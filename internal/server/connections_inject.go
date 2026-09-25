package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"tronbyt-server/internal/connections"
	"tronbyt-server/internal/data"
	"tronbyt-server/internal/renderer"

	"gorm.io/gorm"
)

// oauth2Field is the subset of a pixlet schema.OAuth2 field we care about
// at render time. We deliberately only decode known keys so changes
// upstream don't break parsing.
type oauth2Field struct {
	Type                  string `json:"type"`
	ID                    string `json:"id"`
	AuthorizationEndpoint string `json:"authorization_endpoint"`
}

// schemaOAuth2Fields parses a schema JSON blob and returns the OAuth2
// fields. Returns nil for any malformed schema; callers treat that as "no
// fields to inject" rather than a render error.
func schemaOAuth2Fields(schemaJSON []byte) []oauth2Field {
	if len(schemaJSON) == 0 {
		return nil
	}
	var doc struct {
		Schema []json.RawMessage `json:"schema"`
	}
	if err := json.Unmarshal(schemaJSON, &doc); err != nil {
		return nil
	}
	var out []oauth2Field
	for _, raw := range doc.Schema {
		var f oauth2Field
		if err := json.Unmarshal(raw, &f); err != nil {
			continue
		}
		if f.Type == "oauth2" && f.ID != "" {
			out = append(out, f)
		}
	}
	return out
}

// oauth2FieldsEntry memoizes the oauth2 fields extracted from one version
// of an app. Extracting them requires a full starlark evaluation
// (renderer.GetSchema loads and executes the applet), which is far too
// expensive to repeat on every render on Pi-class hardware — so results
// are cached and revalidated against a cheap fingerprint of the app's
// source files. Negative results (no oauth2 fields — the overwhelmingly
// common case) are cached too.
type oauth2FieldsEntry struct {
	fingerprint string
	fields      []oauth2Field
}

// appFingerprint identifies the current content of an app cheaply enough
// to check on every render. Single-file apps use mtime+size; directory
// apps — which is every app from the system repo — fold in each source
// file, so editing or adding a .star invalidates the entry. A directory's
// own mtime is not enough: it doesn't change when a file inside is
// rewritten in place.
func appFingerprint(appPath string) (string, error) {
	info, err := os.Stat(appPath)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return fmt.Sprintf("f:%d:%d", info.ModTime().UnixNano(), info.Size()), nil
	}

	entries, err := os.ReadDir(appPath) // sorted by filename
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString("d:")
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".star") && name != "manifest.yaml" {
			continue
		}
		fi, err := entry.Info()
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&b, "%s:%d:%d;", name, fi.ModTime().UnixNano(), fi.Size())
	}
	return b.String(), nil
}

// oauth2FieldsForApp returns the oauth2 fields declared by the app at
// appPath, re-evaluating the schema only when the app's sources change.
func (s *Server) oauth2FieldsForApp(ctx context.Context, appPath string) []oauth2Field {
	fingerprint, err := appFingerprint(appPath)
	if err != nil {
		return nil
	}

	s.oauth2FieldsMu.RLock()
	e, ok := s.oauth2FieldsCache[appPath]
	s.oauth2FieldsMu.RUnlock()
	if ok && e.fingerprint == fingerprint {
		return e.fields
	}

	schemaJSON, err := renderer.GetSchema(ctx, appPath, 64, 32, false)
	if err != nil {
		// Don't cache failures. A transient error — a canceled request
		// context, or a get_schema() that fetches over the network — would
		// otherwise be pinned as "this app has no oauth2 fields" for the
		// life of the process, silently dropping token injection while the
		// UI still reports the account as connected.
		slog.Warn("Schema evaluation failed; skipping connection token injection",
			"path", appPath, "error", err)
		return nil
	}
	fields := schemaOAuth2Fields(schemaJSON)

	s.oauth2FieldsMu.Lock()
	if s.oauth2FieldsCache == nil {
		s.oauth2FieldsCache = make(map[string]oauth2FieldsEntry)
	}
	s.oauth2FieldsCache[appPath] = oauth2FieldsEntry{
		fingerprint: fingerprint,
		fields:      fields,
	}
	s.oauth2FieldsMu.Unlock()
	return fields
}

// injectConnectionTokens augments the render config with fresh access
// tokens for any third-party provider declared in the app's schema. It is
// best-effort: if a provider isn't recognised, isn't configured, or the
// user hasn't connected it, we leave the field absent so the starlark app
// can render its own "connect first" prompt.
//
// appPath is the resolved star file path; username is the owner. The
// returned set holds the field IDs that actually received a token —
// handleSchemaHandler uses it to route tokens into one-param handlers.
func (s *Server) injectConnectionTokens(ctx context.Context, config map[string]any, appPath, username string) map[string]bool {
	// A nil config is a legitimate input on the schema-handler path (the
	// request body may omit "config" entirely), and writing to a nil map
	// panics.
	if s.Connections == nil || s.ConnectionsRegistry == nil || config == nil || appPath == "" || username == "" {
		return nil
	}

	fields := s.oauth2FieldsForApp(ctx, appPath)
	if len(fields) == 0 {
		return nil
	}

	injected := make(map[string]bool, len(fields))
	for _, f := range fields {
		provider, ok := s.ConnectionsRegistry.MatchAuthorizeURL(f.AuthorizationEndpoint)
		if !ok {
			continue
		}
		token, err := s.Connections.AccessTokenForUser(ctx, provider.Name, username)
		if err != nil {
			if !errors.Is(err, connections.ErrConnectionNotFound) {
				slog.Warn("Connection token unavailable for render", "provider", provider.Name, "user", username, "error", err)
			}
			continue
		}
		// Pixlet's runtime stringifies config values before they reach
		// starlark, so we hand back a plain access token string. Apps
		// read config[field.id] as a string (the bearer token); they
		// never see the refresh token. Provider-side identifiers like
		// the Strava athlete id are derivable from the token itself.
		config[f.ID] = token
		injected[f.ID] = true
	}
	return injected
}

// annotateSchemaForUI rewrites the schema JSON before it's sent to the
// browser so the config form can render a useful "Connect" button for each
// OAuth2 field. We strip the bake-time client_id (the server controls
// credentials) and add tronbyt-specific hints:
//   - tronbyt_provider:    resolved provider name, if known
//   - tronbyt_connected:   bool — does the user have a live connection?
//   - tronbyt_label:       display name from the connection (e.g. athlete name)
//   - tronbyt_configured:  bool — has the admin set the env-var credentials?
//   - tronbyt_scopes:      space-separated scopes to request
//
// The JS in configapp.html keys off these fields. Apps and pixlet itself
// continue to see the original shape — these extras are additive.
func (s *Server) annotateSchemaForUI(ctx context.Context, schemaJSON []byte, username string) []byte {
	if s.ConnectionsRegistry == nil || len(schemaJSON) == 0 {
		return schemaJSON
	}

	var doc map[string]any
	if err := json.Unmarshal(schemaJSON, &doc); err != nil {
		return schemaJSON
	}
	rawFields, ok := doc["schema"].([]any)
	if !ok {
		return schemaJSON
	}

	mutated := false
	for _, raw := range rawFields {
		field, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		typ, _ := field["type"].(string)
		if typ != "oauth2" {
			continue
		}

		mutated = true
		fieldID, _ := field["id"].(string)
		authzURL, _ := field["authorization_endpoint"].(string)

		provider, providerOK := s.ConnectionsRegistry.MatchAuthorizeURL(authzURL)
		if !providerOK {
			field["tronbyt_provider"] = ""
			field["tronbyt_configured"] = false
			field["tronbyt_connected"] = false
			continue
		}

		// A provider counts as configured if *either* flow can run. The
		// device flow needs no secret, so GitHub is usable with just a
		// client id — the button then points at the code flow instead of
		// a redirect.
		deviceFlow := s.Connections.DeviceFlowAvailable(provider)
		field["tronbyt_provider"] = provider.Name
		field["tronbyt_configured"] = s.Connections.CodeFlowAvailable(provider) || deviceFlow
		field["tronbyt_device_flow"] = deviceFlow
		field["tronbyt_display_name"] = provider.DisplayName

		// Don't leak the bake-time client_id from the app's schema; the
		// server uses its own. The auth flow happens server-side anyway.
		field["client_id"] = ""

		// Pass scopes through as a string the JS can forward.
		switch sc := field["scopes"].(type) {
		case []any:
			parts := make([]string, 0, len(sc))
			for _, v := range sc {
				if s, ok := v.(string); ok {
					parts = append(parts, s)
				}
			}
			field["tronbyt_scopes"] = strings.Join(parts, " ")
		case string:
			field["tronbyt_scopes"] = sc
		default:
			field["tronbyt_scopes"] = ""
		}

		// Look up the user's live connection (if any) for connection state.
		conn, err := gorm.G[data.Connection](s.DB).
			Where("user_id = ? AND provider = ?", username, provider.Name).
			First(ctx)
		if err == nil {
			field["tronbyt_connected"] = true
			field["tronbyt_label"] = conn.DisplayName
			field["tronbyt_connection_id"] = conn.ID
		} else {
			field["tronbyt_connected"] = false
		}
		_ = fieldID
	}

	if !mutated {
		return schemaJSON
	}
	out, err := json.Marshal(doc)
	if err != nil {
		return schemaJSON
	}
	return out
}
