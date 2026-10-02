package server

import (
	"context"
	"encoding/json"
	"testing"

	"tronbyt-server/internal/config"
	"tronbyt-server/internal/connections"
	"tronbyt-server/internal/data"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestSchemaOAuth2Fields(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string // expected field IDs in order
	}{
		{
			name: "single oauth2 field",
			in: `{"version":"1","schema":[
				{"type":"oauth2","id":"strava","authorization_endpoint":"https://www.strava.com/oauth/authorize"}
			]}`,
			want: []string{"strava"},
		},
		{
			name: "mixed schema, oauth2 plus text",
			in: `{"schema":[
				{"type":"text","id":"unit"},
				{"type":"oauth2","id":"auth","authorization_endpoint":"https://accounts.spotify.com/authorize"},
				{"type":"dropdown","id":"sport"}
			]}`,
			want: []string{"auth"},
		},
		{
			name: "no oauth2 fields",
			in:   `{"schema":[{"type":"text","id":"x"}]}`,
			want: nil,
		},
		{
			name: "garbage input returns nil",
			in:   `not json`,
			want: nil,
		},
		{
			name: "empty schema array",
			in:   `{"schema":[]}`,
			want: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := schemaOAuth2Fields([]byte(tc.in))
			ids := make([]string, len(got))
			for i, f := range got {
				ids[i] = f.ID
			}
			if tc.want == nil {
				assert.Empty(t, ids)
				return
			}
			assert.Equal(t, tc.want, ids)
		})
	}
}

// makeAnnotateServer builds the minimal *Server needed to exercise
// annotateSchemaForUI: DB, registry, config. No HTTP, no templates.
func makeAnnotateServer(t *testing.T, cfg *config.Settings) *Server {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=private"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&data.User{}, &data.Connection{}))

	s := &Server{
		DB:                  db,
		Config:              cfg,
		ConnectionsRegistry: connections.NewRegistry(connections.Strava()),
	}
	s.Connections = &connections.Service{
		DB:       db,
		Registry: s.ConnectionsRegistry,
		Secret:   "test-secret",
		GetCreds: cfg.ConnectionClientCreds,
	}
	return s
}

func TestAnnotateSchemaForUI_UnconfiguredProvider(t *testing.T) {
	s := makeAnnotateServer(t, &config.Settings{}) // no STRAVA_* set

	in := []byte(`{"version":"1","schema":[
		{"type":"oauth2","id":"strava","name":"Strava Login",
		 "client_id":"baked-in-id-from-app",
		 "authorization_endpoint":"https://www.strava.com/oauth/authorize",
		 "scopes":["read","activity:read"]}
	]}`)

	out := s.annotateSchemaForUI(context.Background(), in, "alice")

	field := firstSchemaField(t, out)
	assert.Equal(t, "strava", field["tronbyt_provider"])
	assert.Equal(t, false, field["tronbyt_configured"])
	assert.Equal(t, false, field["tronbyt_connected"])
	assert.Equal(t, "Strava", field["tronbyt_display_name"])
	assert.Equal(t, "read activity:read", field["tronbyt_scopes"])
	// The bake-time client_id must be wiped — server uses its own.
	assert.Equal(t, "", field["client_id"])
}

func TestAnnotateSchemaForUI_ConfiguredButNotConnected(t *testing.T) {
	cfg := &config.Settings{
		StravaClientID:     "real-id",
		StravaClientSecret: "real-secret",
	}
	s := makeAnnotateServer(t, cfg)
	require.NoError(t, s.DB.Create(&data.User{Username: "alice", APIKey: "k"}).Error)

	in := []byte(`{"schema":[
		{"type":"oauth2","id":"strava","authorization_endpoint":"https://www.strava.com/oauth/authorize"}
	]}`)
	out := s.annotateSchemaForUI(context.Background(), in, "alice")

	field := firstSchemaField(t, out)
	assert.Equal(t, true, field["tronbyt_configured"])
	assert.Equal(t, false, field["tronbyt_connected"])
}

func TestAnnotateSchemaForUI_Connected(t *testing.T) {
	cfg := &config.Settings{
		StravaClientID:     "real-id",
		StravaClientSecret: "real-secret",
	}
	s := makeAnnotateServer(t, cfg)
	require.NoError(t, s.DB.Create(&data.User{Username: "alice", APIKey: "k"}).Error)

	// Pretend alice has already connected Strava.
	require.NoError(t, s.DB.Create(&data.Connection{
		UserID:      "alice",
		Provider:    "strava",
		ExternalID:  "12345",
		DisplayName: "Alice Athlete",
	}).Error)

	in := []byte(`{"schema":[
		{"type":"oauth2","id":"strava","authorization_endpoint":"https://www.strava.com/oauth/authorize"}
	]}`)
	out := s.annotateSchemaForUI(context.Background(), in, "alice")

	field := firstSchemaField(t, out)
	assert.Equal(t, true, field["tronbyt_connected"])
	assert.Equal(t, "Alice Athlete", field["tronbyt_label"])
	// connection_id is round-tripped via JSON, so it'll come back as float64.
	id, ok := field["tronbyt_connection_id"].(float64)
	assert.True(t, ok)
	assert.Greater(t, id, 0.0)
}

func TestAnnotateSchemaForUI_UnknownProvider(t *testing.T) {
	s := makeAnnotateServer(t, &config.Settings{})

	in := []byte(`{"schema":[
		{"type":"oauth2","id":"weirdo","authorization_endpoint":"https://example.com/oauth/authorize"}
	]}`)
	out := s.annotateSchemaForUI(context.Background(), in, "alice")

	field := firstSchemaField(t, out)
	assert.Equal(t, "", field["tronbyt_provider"])
	assert.Equal(t, false, field["tronbyt_configured"])
	assert.Equal(t, false, field["tronbyt_connected"])
}

func TestAnnotateSchemaForUI_NoOAuth2FieldsLeavesSchemaAlone(t *testing.T) {
	s := makeAnnotateServer(t, &config.Settings{})

	in := []byte(`{"schema":[{"type":"text","id":"hello"}]}`)
	out := s.annotateSchemaForUI(context.Background(), in, "alice")

	// Byte-identical when there's nothing to annotate; cheaper for the
	// common case and makes diffs easier to read.
	assert.Equal(t, string(in), string(out))
}

// firstSchemaField extracts schema[0] as a generic map for assertions.
func firstSchemaField(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var doc struct {
		Schema []map[string]any `json:"schema"`
	}
	require.NoError(t, json.Unmarshal(raw, &doc))
	require.NotEmpty(t, doc.Schema)
	return doc.Schema[0]
}
