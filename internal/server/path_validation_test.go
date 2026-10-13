package server

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tronbyt-server/internal/data"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestIsValidUsername(t *testing.T) {
	for _, name := range []string{"admin", "Alice", "user1", "jane.doe", "jane_doe", "jane-doe", "jane+tag@example.com", "1"} {
		assert.True(t, isValidUsername(name), "expected %q to be valid", name)
	}
	for _, name := range []string{"", ".", "..", "../admin", "a/b", `a\b`, ".hidden", "-dash", "has space", "colon:name", strings.Repeat("a", 129)} {
		assert.False(t, isValidUsername(name), "expected %q to be invalid", name)
	}
}

func TestHandleRegisterPost_RejectsInvalidUsername(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()

	for _, username := range []string{"..", "../users/alice", "a/b"} {
		form := url.Values{}
		form.Add("username", username)
		form.Add("password", "password123")

		req, _ := http.NewRequest(http.MethodPost, "/auth/register", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

		rr := httptest.NewRecorder()
		http.HandlerFunc(s.handleRegisterPost).ServeHTTP(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code, "username %q", username)
		count, err := gorm.G[data.User](s.DB).Where("username = ?", username).Count(ctx, "*")
		require.NoError(t, err)
		assert.Zero(t, count, "user %q should not have been created", username)
	}
}

// Deleting a user whose stored name predates validation only removes that
// user's own files.
func TestHandleDeleteUser_LegacyUsername(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()

	// api_key is unique, so each user needs its own.
	admin := data.User{Username: "admin", IsAdmin: true, APIKey: "admin-key"}
	require.NoError(t, gorm.G[data.User](s.DB).Create(ctx, &admin))
	bad := data.User{Username: "..", APIKey: "bad-key"}
	require.NoError(t, gorm.G[data.User](s.DB).Create(ctx, &bad))
	// Older versions could create a device with an empty ID.
	require.NoError(t, gorm.G[data.Device](s.DB).Create(ctx, &data.Device{ID: "", Username: ".."}))

	sentinel := filepath.Join(s.DataDir, "keep-me")
	require.NoError(t, os.WriteFile(sentinel, []byte("x"), 0644))
	otherImage := filepath.Join(s.DataDir, "webp", "otherdevice", "img.webp")
	require.NoError(t, os.MkdirAll(filepath.Dir(otherImage), 0755))
	require.NoError(t, os.WriteFile(otherImage, []byte("x"), 0644))

	req, _ := http.NewRequest(http.MethodPost, "/admin/users/../delete", nil)
	req.SetPathValue("username", "..")
	req = req.WithContext(context.WithValue(req.Context(), userContextKey, &admin))

	rr := httptest.NewRecorder()
	http.HandlerFunc(s.handleDeleteUser).ServeHTTP(rr, req)

	assert.FileExists(t, sentinel, "files outside the user's directory are kept")
	assert.FileExists(t, otherImage, "other devices' images are kept")
	count, err := gorm.G[data.User](s.DB).Where("username = ?", "..").Count(ctx, "*")
	require.NoError(t, err)
	assert.Zero(t, count, "user should still be deleted from the database")
}

func TestEnsureDeviceImageDir_RejectsUnsafeIDs(t *testing.T) {
	s := newTestServer(t)
	for _, id := range []string{"", ".", ".."} {
		_, err := s.ensureDeviceImageDir(id)
		assert.Error(t, err, "device ID %q", id)
	}
}

func TestUniqueDeviceIDFromName(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()

	id, err := s.uniqueDeviceIDFromName(ctx, "Kitchen")
	require.NoError(t, err)
	assert.Equal(t, "kitchen", id)

	// Device IDs are global: another user's "Kitchen" must not collide.
	require.NoError(t, gorm.G[data.Device](s.DB).Create(ctx, &data.Device{ID: "kitchen", Username: "someone-else"}))
	id, err = s.uniqueDeviceIDFromName(ctx, "Kitchen")
	require.NoError(t, err)
	assert.Equal(t, "kitchen-2", id)

	// A name with no letters or digits must not produce an empty ID.
	for _, name := range []string{"!!!", "🎉", "---"} {
		id, err := s.uniqueDeviceIDFromName(ctx, name)
		require.NoError(t, err)
		assert.Len(t, id, 8, "name %q", name)
		assert.Regexp(t, validDeviceIDRe, id)
	}
}

func TestHandleCreateDevicePost_FromNameWithoutAlphanumerics(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()

	user := data.User{Username: "testuser"}
	require.NoError(t, gorm.G[data.User](s.DB).Create(ctx, &user))

	form := url.Values{}
	form.Add("name", "🎉🎉")
	form.Add("device_type", "tidbyt_gen1")
	form.Add("brightness", "2")
	form.Add("device_id_mode", "from_name")

	req, _ := http.NewRequest(http.MethodPost, "/devices/create", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(context.WithValue(req.Context(), userContextKey, &user))

	rr := httptest.NewRecorder()
	http.HandlerFunc(s.handleCreateDevicePost).ServeHTTP(rr, req)
	require.Equal(t, http.StatusSeeOther, rr.Code, "body: %s", rr.Body.String())

	device, err := gorm.G[data.Device](s.DB).Where("name = ?", "🎉🎉").First(ctx)
	require.NoError(t, err)
	assert.NotEmpty(t, device.ID)
}

// Uploads whose app name is empty or starts with a dot are rejected.
func TestHandleUploadAppPost_RejectsDotNames(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()

	user := data.User{Username: "testuser"}
	require.NoError(t, gorm.G[data.User](s.DB).Create(ctx, &user))
	device := data.Device{ID: "testdevice", Username: "testuser"}
	require.NoError(t, gorm.G[data.Device](s.DB).Create(ctx, &device))

	existing := filepath.Join(s.DataDir, "users", "testuser", "apps", "myapp", "myapp.star")
	require.NoError(t, os.MkdirAll(filepath.Dir(existing), 0755))
	require.NoError(t, os.WriteFile(existing, []byte("x"), 0644))

	for _, filename := range []string{".zip", "..zip", ".star"} {
		body := &bytes.Buffer{}
		writer := multipart.NewWriter(body)
		part, err := writer.CreateFormFile("file", filename)
		require.NoError(t, err)
		_, err = part.Write([]byte("not really a zip"))
		require.NoError(t, err)
		require.NoError(t, writer.Close())

		req, _ := http.NewRequest(http.MethodPost, "/devices/testdevice/uploadapp", body)
		req.Header.Set("Content-Type", writer.FormDataContentType())
		reqCtx := context.WithValue(req.Context(), userContextKey, &user)
		reqCtx = context.WithValue(reqCtx, deviceContextKey, &device)
		req = req.WithContext(reqCtx)

		rr := httptest.NewRecorder()
		http.HandlerFunc(s.handleUploadAppPost).ServeHTTP(rr, req)

		assert.Equal(t, http.StatusBadRequest, rr.Code, "filename %q", filename)
		assert.FileExists(t, existing, "existing app is kept after upload of %q", filename)
	}
}

func TestUserAppDir(t *testing.T) {
	root := t.TempDir()

	dir, err := userAppDir(root, "myapp")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(root, "myapp"), dir)

	for _, name := range []string{"", " ", ".", "..", ".hidden", "../other"} {
		_, err := userAppDir(root, name)
		assert.Error(t, err, "app name %q", name)
	}
}
