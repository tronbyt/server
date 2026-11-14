package server

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"strings"

	"tronbyt-server/internal/data"
	"tronbyt-server/internal/oauth"

	"gorm.io/gorm"
)

// oauthClientIDLength is the length of an OAuthClient.ID, in hex characters.
const oauthClientIDLength = 16

var oauthClientIDRe = regexp.MustCompile(`^[0-9a-f]{16}$`)

// oauthClientRow is one client in the settings list.
type oauthClientRow struct {
	ID                    string
	Name                  string
	OwnerUsername         string
	Shared                bool
	AuthorizationEndpoint string
	TokenEndpoint         string
	ClientID              string
	HasSecret             bool
	RedirectURI           string
	CanManage             bool
	Connections           int64
}

// oauthClientForm is the create/edit form on the settings page. The ID is
// chosen before the client is saved, so the redirect URI can be registered
// with the provider while the form is being filled in.
type oauthClientForm struct {
	ID                    string
	Editing               bool
	Name                  string
	AuthorizationEndpoint string
	TokenEndpoint         string
	ClientID              string
	HasSecret             bool
	Shared                bool
	RedirectURI           string
	ReturnTo              string
}

func (s *Server) handleOAuthClientsGet(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r)
	form, err := s.oauthClientFormFor(r.Context(), r, user)
	if err != nil {
		slog.Error("Failed to prepare OAuth client form", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	s.renderOAuthClientsPage(w, r, user, form, nil)
}

// renderOAuthClientsPage renders the client list with the given form.
func (s *Server) renderOAuthClientsPage(w http.ResponseWriter, r *http.Request, user *data.User, form oauthClientForm, flashes []string) {
	ctx := r.Context()

	clients, err := gorm.G[data.OAuthClient](s.DB).
		Where("owner_username = ? OR shared = ?", user.Username, true).
		Order("name ASC").
		Find(ctx)
	if err != nil {
		slog.Error("Failed to load OAuth clients", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	rows := make([]oauthClientRow, 0, len(clients))
	for i := range clients {
		c := &clients[i]
		connections, err := gorm.G[data.OAuthToken](s.DB).Where("oauth_client_id = ?", c.ID).Count(ctx, "*")
		if err != nil {
			slog.Error("Failed to count OAuth connections", "client_id", c.ID, "error", err)
		}
		rows = append(rows, oauthClientRow{
			ID:                    c.ID,
			Name:                  c.Name,
			OwnerUsername:         c.OwnerUsername,
			Shared:                c.Shared,
			AuthorizationEndpoint: c.AuthorizationEndpoint,
			TokenEndpoint:         c.TokenEndpoint,
			ClientID:              c.ClientID,
			HasSecret:             c.ClientSecret != "",
			RedirectURI:           s.oauthRedirectURI(r, c.ID),
			CanManage:             canManageOAuthClient(user, c),
			Connections:           connections,
		})
	}

	s.renderTemplate(w, r, "settings_oauth", TemplateData{
		User:            user,
		SettingsSection: "oauth",
		OAuthClients:    rows,
		OAuthClientForm: form,
		Flashes:         flashes,
	})
}

// rejectOAuthClientForm shows the form again with what the user submitted and
// an error. It keeps the client's ID, so the redirect URI the user may already
// have registered with the provider stays valid. The secret is not echoed back.
func (s *Server) rejectOAuthClientForm(w http.ResponseWriter, r *http.Request, user *data.User, message string) {
	form := oauthClientForm{
		ID:                    r.FormValue("id"),
		Name:                  strings.TrimSpace(r.FormValue("name")),
		AuthorizationEndpoint: strings.TrimSpace(r.FormValue("authorization_endpoint")),
		TokenEndpoint:         strings.TrimSpace(r.FormValue("token_endpoint")),
		ClientID:              strings.TrimSpace(r.FormValue("client_id")),
		Shared:                user.IsAdmin && r.FormValue("shared") == "on",
		ReturnTo:              safeReturnPath(r.FormValue("return_to")),
	}
	if oauthClientIDRe.MatchString(form.ID) {
		existing, err := gorm.G[data.OAuthClient](s.DB).Where("id = ?", form.ID).First(r.Context())
		switch {
		case err == nil:
			if !canManageOAuthClient(user, &existing) {
				http.Error(w, "Forbidden", http.StatusForbidden)
				return
			}
			form.Editing = true
			form.HasSecret = existing.ClientSecret != ""
			if !user.IsAdmin {
				form.Shared = existing.Shared
			}
		case !errors.Is(err, gorm.ErrRecordNotFound):
			slog.Error("Failed to load OAuth client", "error", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
	} else {
		id, err := generateSecureToken(oauthClientIDLength)
		if err != nil {
			slog.Error("Failed to generate OAuth client ID", "error", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		form.ID = id
	}
	form.RedirectURI = s.oauthRedirectURI(r, form.ID)
	s.renderOAuthClientsPage(w, r, user, form, []string{message})
}

// oauthClientFormFor fills the form from ?edit=<id>, or for a new client from
// the query the config page links with (name, endpoints, return_to).
func (s *Server) oauthClientFormFor(ctx context.Context, r *http.Request, user *data.User) (oauthClientForm, error) {
	query := r.URL.Query()
	returnTo := safeReturnPath(query.Get("return_to"))

	if editID := query.Get("edit"); editID != "" {
		client, err := gorm.G[data.OAuthClient](s.DB).Where("id = ?", editID).First(ctx)
		if err == nil && canManageOAuthClient(user, &client) {
			return oauthClientForm{
				ID:                    client.ID,
				Editing:               true,
				Name:                  client.Name,
				AuthorizationEndpoint: client.AuthorizationEndpoint,
				TokenEndpoint:         client.TokenEndpoint,
				ClientID:              client.ClientID,
				HasSecret:             client.ClientSecret != "",
				Shared:                client.Shared,
				RedirectURI:           s.oauthRedirectURI(r, client.ID),
				ReturnTo:              returnTo,
			}, nil
		}
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return oauthClientForm{}, err
		}
	}

	id, err := generateSecureToken(oauthClientIDLength)
	if err != nil {
		return oauthClientForm{}, err
	}
	return oauthClientForm{
		ID:                    id,
		Name:                  query.Get("name"),
		AuthorizationEndpoint: query.Get("authorization_endpoint"),
		TokenEndpoint:         query.Get("token_endpoint"),
		RedirectURI:           s.oauthRedirectURI(r, id),
		ReturnTo:              returnTo,
	}, nil
}

// handleOAuthClientSave creates a client, or updates it when the ID exists.
func (s *Server) handleOAuthClientSave(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r)
	ctx := r.Context()

	id := r.FormValue("id")
	name := strings.TrimSpace(r.FormValue("name"))
	clientID := strings.TrimSpace(r.FormValue("client_id"))
	secret := r.FormValue("client_secret")
	returnTo := safeReturnPath(r.FormValue("return_to"))
	localizer := s.getLocalizer(r)

	if !oauthClientIDRe.MatchString(id) || name == "" || len(name) > 100 || clientID == "" {
		s.rejectOAuthClientForm(w, r, user, s.localizeOrID(localizer, "Please fill in the name and client ID."))
		return
	}
	// Requests go to the endpoints as entered: a provider may need a trailing
	// slash. The canonical forms only decide which apps the client matches.
	authEndpoint := strings.TrimSpace(r.FormValue("authorization_endpoint"))
	tokenEndpoint := strings.TrimSpace(r.FormValue("token_endpoint"))
	authCanonical, err := oauth.CanonicalEndpoint(authEndpoint)
	if err != nil {
		s.rejectOAuthClientForm(w, r, user, s.localizeOrID(localizer, "Invalid authorization endpoint:")+" "+err.Error())
		return
	}
	tokenCanonical, err := oauth.CanonicalEndpoint(tokenEndpoint)
	if err != nil {
		s.rejectOAuthClientForm(w, r, user, s.localizeOrID(localizer, "Invalid token endpoint:")+" "+err.Error())
		return
	}
	issuerKey := authCanonical + " " + tokenCanonical

	err = s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		existing, err := gorm.G[data.OAuthClient](tx).Where("id = ?", id).First(ctx)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return gorm.G[data.OAuthClient](tx).Create(ctx, &data.OAuthClient{
				ID:                    id,
				Name:                  name,
				OwnerUsername:         user.Username,
				Shared:                user.IsAdmin && r.FormValue("shared") == "on",
				AuthorizationEndpoint: authEndpoint,
				TokenEndpoint:         tokenEndpoint,
				IssuerKey:             issuerKey,
				ClientID:              clientID,
				ClientSecret:          data.Secret(secret),
			})
		}
		if err != nil {
			return err
		}
		if !canManageOAuthClient(user, &existing) {
			return errOAuthClientForbidden
		}

		shared := existing.Shared
		if user.IsAdmin {
			shared = r.FormValue("shared") == "on"
		}
		switch {
		case existing.IssuerKey != issuerKey:
			// Tokens came from the old provider; refreshing them at the new
			// token endpoint would hand them to a different server.
			if _, err := gorm.G[data.OAuthToken](tx).Where("oauth_client_id = ?", id).Delete(ctx); err != nil {
				return err
			}
		case existing.Shared && !shared:
			// Other users may no longer use the client.
			if _, err := gorm.G[data.OAuthToken](tx).
				Where("oauth_client_id = ? AND device_id NOT IN (?)", id,
					tx.Model(&data.Device{}).Select("id").Where("username = ?", existing.OwnerUsername)).
				Delete(ctx); err != nil {
				return err
			}
		}

		updated := data.OAuthClient{
			Name:                  name,
			Shared:                shared,
			AuthorizationEndpoint: authEndpoint,
			TokenEndpoint:         tokenEndpoint,
			IssuerKey:             issuerKey,
			ClientID:              clientID,
			ClientSecret:          existing.ClientSecret,
		}
		// The secret is write-only: an empty field keeps the stored one.
		if secret != "" {
			updated.ClientSecret = data.Secret(secret)
		}
		_, err = gorm.G[data.OAuthClient](tx).Where("id = ?", id).
			Select("Name", "Shared", "AuthorizationEndpoint", "TokenEndpoint", "IssuerKey", "ClientID", "ClientSecret").
			Updates(ctx, updated)
		return err
	})
	if errors.Is(err, errOAuthClientForbidden) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if err != nil {
		slog.Error("Failed to save OAuth client", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	redirect := "/settings/oauth"
	if returnTo != "" {
		redirect = returnTo
	}
	s.flashAndRedirect(w, r, "OAuth client saved.", redirect, http.StatusSeeOther)
}

var errOAuthClientForbidden = errors.New("not allowed to manage this OAuth client")

func (s *Server) handleOAuthClientDelete(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r)
	ctx := r.Context()

	client, err := gorm.G[data.OAuthClient](s.DB).Where("id = ?", r.PathValue("client")).First(ctx)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			http.NotFound(w, r)
			return
		}
		slog.Error("Failed to load OAuth client", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	if !canManageOAuthClient(user, &client) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	if err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return deleteOAuthClients(ctx, tx, []string{client.ID})
	}); err != nil {
		slog.Error("Failed to delete OAuth client", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	s.flashAndRedirect(w, r, "OAuth client deleted.", "/settings/oauth", http.StatusSeeOther)
}

// safeReturnPath keeps a same-site path, or returns "".
func safeReturnPath(raw string) string {
	if raw == "" || !strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") || strings.HasPrefix(raw, "/\\") || strings.ContainsAny(raw, "\r\n") {
		return ""
	}
	return raw
}
