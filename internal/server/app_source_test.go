package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"tronbyt-server/internal/apps"
	"tronbyt-server/internal/data"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSplitAppPath(t *testing.T) {
	tests := []struct {
		path     string
		wantBase string
		wantRel  string
		wantErr  bool
	}{
		{path: "system-apps/apps/clock", wantBase: "system-apps", wantRel: "apps/clock"},
		{path: "system-apps/apps/clock/clock.star", wantBase: "system-apps", wantRel: "apps/clock/clock.star"},
		{path: "users/alice/apps/x/x.star", wantBase: "users/alice", wantRel: "apps/x/x.star"},
		{path: "users/alice/repo/apps/x/x.webp", wantBase: "users/alice", wantRel: "repo/apps/x/x.webp"},
		{path: "users/bob/apps/x/x.webp", wantErr: true},
		{path: "users/alice/../bob/apps/x/x.webp", wantErr: true},
		{path: "users/alice", wantErr: true},
		{path: "system-apps", wantErr: true},
		{path: "tronbyt.db", wantErr: true},
		{path: "webp/otherdevice/img.webp", wantErr: true},
		{path: "../etc/passwd", wantErr: true},
		{path: "/etc/passwd", wantErr: true},
		{path: "", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			base, rel, err := splitAppPath("alice", tt.path)
			if tt.wantErr {
				assert.ErrorIs(t, err, errAppPathForbidden)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantBase, base)
			assert.Equal(t, tt.wantRel, rel)
		})
	}

	_, _, err := splitAppPath("", "users//apps/x/x.star")
	assert.ErrorIs(t, err, errAppPathForbidden, "empty owner must not match")
}

// setupAppSourceFixture lays out a DataDir with a secret database, a victim's private app,
// and an attacker's repo containing symlinks that point outside the attacker's directory.
func setupAppSourceFixture(t *testing.T, s *Server) {
	t.Helper()

	write := func(rel, content string) {
		p := filepath.Join(s.DataDir, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0755))
		require.NoError(t, os.WriteFile(p, []byte(content), 0644))
	}
	symlink := func(target, rel string) {
		p := filepath.Join(s.DataDir, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0755))
		if err := os.Symlink(target, p); err != nil {
			if runtime.GOOS == "windows" {
				t.Skipf("creating symlinks requires Developer Mode or elevation on Windows: %v", err)
			}
			require.NoError(t, err)
		}
		// Make sure each symlink resolves, so tests fail because of os.Root and not a bad fixture.
		_, err := os.Stat(p)
		if target != "/tronbyt.db" {
			require.NoError(t, err, "fixture symlink %s -> %s does not resolve", rel, target)
		}
	}

	write("tronbyt.db", "SECRET DATABASE")
	write("users/victim/apps/secret/secret.webp", "VICTIM IMAGE")
	write("users/attacker/apps/mine/mine.webp", "RIFF....WEBP")

	symlink("/tronbyt.db", "users/attacker/repo/apps/abs/abs.webp")
	symlink("../../../../../tronbyt.db", "users/attacker/repo/apps/rel/rel.webp")
	symlink("../../../victim/apps/secret", "users/attacker/repo/apps/dir")
	// Symlinks are confined to the app's own directory, even within the owner's tree.
	symlink("../../../apps/mine/mine.webp", "users/attacker/repo/apps/sibling/sibling.webp")
	write("users/attacker/repo/apps/ok/real.webp", "RIFF....WEBP")
	symlink("real.webp", "users/attacker/repo/apps/ok/ok.webp")
}

func TestOpenAppSource(t *testing.T) {
	s := newTestServer(t)
	setupAppSourceFixture(t, s)

	for _, p := range []string{
		"tronbyt.db",
		"users/victim/apps/secret/secret.webp",
		"users/attacker/repo/apps/abs/abs.webp",
		"users/attacker/repo/apps/rel/rel.webp",
		"users/attacker/repo/apps/dir/secret.webp",
		"users/attacker/repo/apps/sibling/sibling.webp",
	} {
		t.Run(p, func(t *testing.T) {
			src, err := s.openAppSource("attacker", p)
			if err != nil {
				return
			}
			defer func() { _ = src.Close() }()
			b, err := src.ReadFile()
			assert.Error(t, err, "read %q", b)
		})
	}

	src, err := s.openAppSource("attacker", "users/attacker/repo/apps/ok/ok.webp")
	require.NoError(t, err)
	defer func() { _ = src.Close() }()
	assert.True(t, src.IsWebP())
	b, err := src.ReadFile()
	require.NoError(t, err)
	assert.Equal(t, "RIFF....WEBP", string(b))
}

func TestHandleAddAppPost_RejectsPathOutsideOwnerDir(t *testing.T) {
	s := newTestServer(t)
	setupAppSourceFixture(t, s)

	user := data.User{Username: "attacker"}
	s.DB.Create(&user)
	device := data.Device{ID: "attackerdev", Username: "attacker"}
	s.DB.Create(&device)

	for _, p := range []string{
		"tronbyt.db",
		"webp/otherdevice/img.webp",
		"users/victim/apps/secret/secret.webp",
		"users/attacker/repo/apps/abs/abs.webp",
		"users/attacker/repo/apps/rel/rel.webp",
		"users/attacker/repo/apps/dir/secret.webp",
	} {
		form := url.Values{}
		form.Add("name", "x")
		form.Add("path", p)

		req, _ := http.NewRequest(http.MethodPost, "/devices/attackerdev/addapp", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		ctx := context.WithValue(req.Context(), userContextKey, &user)
		ctx = context.WithValue(ctx, deviceContextKey, &device)
		req = req.WithContext(ctx)

		rr := httptest.NewRecorder()
		http.HandlerFunc(s.handleAddAppPost).ServeHTTP(rr, req)
		assert.Equal(t, http.StatusBadRequest, rr.Code, p)
	}

	var count int64
	s.DB.Model(&data.App{}).Where("device_id = ?", device.ID).Count(&count)
	assert.Equal(t, int64(0), count)

	entries, _ := os.ReadDir(filepath.Join(s.DataDir, "webp", device.ID))
	assert.Empty(t, entries, "no files may be copied into the device image dir")
}

// Imported configs store App.Path verbatim, so background rendering must apply the same checks.
func TestPossiblyRender_RejectsPathOutsideOwnerDir(t *testing.T) {
	s := newTestServer(t)
	setupAppSourceFixture(t, s)

	device := data.Device{ID: "attackerdev", Username: "attacker"}
	s.DB.Create(&device)

	for _, p := range []string{
		"users/victim/apps/secret/secret.webp",
		"users/attacker/repo/apps/rel/rel.webp",
	} {
		app := data.App{DeviceID: device.ID, Iname: "imp", Name: "x", Path: &p}
		assert.False(t, s.possiblyRender(t.Context(), &app, &device, nil), p)
	}

	entries, _ := os.ReadDir(filepath.Join(s.DataDir, "webp", device.ID))
	assert.Empty(t, entries, "no files may be copied into the device image dir")
}

func TestGetAppMetadata_ManifestFallbackConfinedToOwner(t *testing.T) {
	s := newTestServer(t)

	for _, u := range []string{"alice", "bob"} {
		p := filepath.Join(s.DataDir, "users", u, "apps", "x", "manifest.yaml")
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0755))
		require.NoError(t, os.WriteFile(p, []byte("name: "+u+"\n"), 0644))
		require.NoError(t, os.WriteFile(filepath.Join(filepath.Dir(p), "x.star"), nil, 0644))
	}

	meta := s.getAppMetadata(t.Context(), "alice", "users/alice/apps/x/x.star")
	require.NotNil(t, meta)
	assert.Equal(t, "alice", meta.Name)

	assert.Nil(t, s.getAppMetadata(t.Context(), "alice", "users/bob/apps/x/x.star"))
}

// setupRepoSymlinkFixture gives the victim a private app and points the attacker's repo
// apps directory, and a repo app's manifest, at it.
func setupRepoSymlinkFixture(t *testing.T, s *Server) {
	t.Helper()

	victimApp := filepath.Join(s.DataDir, "users", "victim", "apps", "secret")
	require.NoError(t, os.MkdirAll(victimApp, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(victimApp, "secret.webp"), []byte("VICTIM IMAGE"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(victimApp, "manifest.yaml"), []byte("summary: victim summary\n"), 0644))

	ownApp := filepath.Join(s.DataDir, "users", "attacker", "apps", "mine")
	require.NoError(t, os.MkdirAll(ownApp, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(ownApp, "mine.webp"), []byte("OWN IMAGE"), 0644))
	if err := os.Symlink("../../../victim/apps/secret/manifest.yaml", filepath.Join(ownApp, "manifest.yaml")); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("creating symlinks requires Developer Mode or elevation on Windows: %v", err)
		}
		require.NoError(t, err)
	}

	repo := filepath.Join(s.DataDir, "users", "attacker", "repo")
	require.NoError(t, os.MkdirAll(repo, 0755))
	require.NoError(t, os.Symlink("../../victim/apps", filepath.Join(repo, "apps")))
	_, err := os.Stat(filepath.Join(repo, "apps", "secret", "secret.webp"))
	require.NoError(t, err, "fixture symlink does not resolve")
}

func TestListUserApps_IgnoresRepoSymlinks(t *testing.T) {
	s := newTestServer(t)
	setupRepoSymlinkFixture(t, s)

	userApps := apps.ListUserApps(s.DataDir, "attacker")
	require.Len(t, userApps, 1, "only the attacker's own uploaded app should be listed")
	assert.Equal(t, "mine", userApps[0].ID)
	assert.NotEqual(t, "victim summary", userApps[0].Summary, "symlinked manifest must not be read")
}

func TestHandleAppThumbnail_ConfinedToOwner(t *testing.T) {
	s := newTestServer(t)
	setupRepoSymlinkFixture(t, s)

	user := data.User{Username: "attacker"}
	get := func(id string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/apps/"+id+"/thumbnail", nil)
		req.SetPathValue("id", id)
		req = req.WithContext(context.WithValue(req.Context(), userContextKey, &user))
		rr := httptest.NewRecorder()
		s.handleAppThumbnail(rr, req)
		return rr
	}

	rr := get("mine")
	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "OWN IMAGE", rr.Body.String())

	rr = get("secret")
	assert.Equal(t, http.StatusNotFound, rr.Code)
	assert.NotContains(t, rr.Body.String(), "VICTIM IMAGE")
}
