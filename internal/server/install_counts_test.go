package server

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/nicksnyder/go-i18n/v2/i18n"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"tronbyt-server/internal/apps"
)

func TestNibletSyncCachesForADay(t *testing.T) {
	s := newTestServer(t)
	calls := 0
	cloud := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		assert.Equal(t, "GET", r.Method)
		assert.Equal(t, "/v1/catalog/install-counts", r.URL.Path)
		assert.Empty(t, r.Header.Get("Authorization"))
		_, _ = w.Write([]byte(`{"protocol_version":"app-install-counts.v1","niblet":{"clock":42}}`))
	}))
	defer cloud.Close()
	s.Config.NibletCloudURL = cloud.URL
	require.NoError(t, s.syncNiblet(context.Background()))
	require.NoError(t, s.syncNiblet(context.Background()))
	assert.Equal(t, 1, calls, "cached counts must not trigger another request")
	assert.Equal(t, int64(42), s.installCounts["clock"])
	next, err := s.getSetting("niblet_next_sync")
	require.NoError(t, err)
	due, err := time.Parse(time.RFC3339, next)
	require.NoError(t, err)
	assert.InDelta(t, float64(24*time.Hour), float64(time.Until(due)), float64(2*time.Second))

	// A restarted server loads the persisted counts without calling Cloud.
	s.installCounts = nil
	require.NoError(t, s.syncNiblet(context.Background()))
	assert.Equal(t, 1, calls)
	assert.Equal(t, int64(42), s.installCounts["clock"])
}

func TestNibletRetryBackoffAndCacheReplacement(t *testing.T) {
	s := newTestServer(t)
	calls := 0
	status := 200
	payload := `{"protocol_version":"app-install-counts.v1","niblet":{"clock":42}}`
	cloud := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(status)
		_, _ = w.Write([]byte(payload))
	}))
	defer cloud.Close()
	s.Config.NibletCloudURL = cloud.URL
	require.NoError(t, s.syncNiblet(context.Background()))
	payload = `{"protocol_version":"app-install-counts.v1","niblet":{"weather":0}}`
	require.NoError(t, s.setSetting("niblet_next_sync", ""))
	require.NoError(t, s.syncNiblet(context.Background()))
	assert.Equal(t, map[string]int64{"weather": 0}, s.installCounts, "removed IDs must disappear")
	status = 503
	require.NoError(t, s.setSetting("niblet_next_sync", ""))
	require.Error(t, s.syncNiblet(context.Background()))
	before := calls
	require.NoError(t, s.syncNiblet(context.Background()))
	assert.Equal(t, before, calls, "failures must respect the persistent hourly backoff")
	assert.Equal(t, map[string]int64{"weather": 0}, s.installCounts, "failures keep the last good counts")
}

func TestNibletRejectsRemotePlainHTTP(t *testing.T) {
	s := newTestServer(t)
	s.Config.NibletCloudURL = "http://cloud.example.com"
	_, err := s.fetchNibletCounts(context.Background())
	require.Error(t, err)
}

func TestNibletCountCardRendersFullNumbers(t *testing.T) {
	s := newTestServer(t)
	for count, want := range map[int64]string{0: "–", 433: "433", 12345: "12,345", 1234567: "1,234,567"} {
		var output bytes.Buffer
		err := s.BaseTemplates.ExecuteTemplate(&output, "app_card_grid_item", map[string]any{
			"App":       apps.AppMetadata{Manifest: apps.Manifest{ID: "clock", Name: "Clock"}, InstallCount: &count},
			"Localizer": i18n.NewLocalizer(s.Bundle, "en"),
			"IsCustom":  false, "ConfigProduction": true, "DeviceID": "test",
		})
		require.NoError(t, err)
		assert.Contains(t, output.String(), "<span>"+want+"</span>")
		label := want
		if count == 0 {
			label = "0" // Screen readers hear the number, not a dash.
		}
		assert.Contains(t, output.String(), `aria-label="Number of installs: `+label+`"`)
		assert.NotContains(t, output.String(), "Niblet")
	}
}

func TestNibletCountCardHiddenWithoutCount(t *testing.T) {
	s := newTestServer(t)
	var output bytes.Buffer
	err := s.BaseTemplates.ExecuteTemplate(&output, "app_card_grid_item", map[string]any{
		"App":       apps.AppMetadata{Manifest: apps.Manifest{ID: "custom", Name: "Custom"}},
		"Localizer": i18n.NewLocalizer(s.Bundle, "en"),
		"IsCustom":  true, "ConfigProduction": true, "DeviceID": "test",
	})
	require.NoError(t, err)
	assert.NotContains(t, output.String(), "app-item-installs")
}
