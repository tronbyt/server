package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"tronbyt-server/internal/data"
)

func TestNibletCloudDisabledSkipsInstallCountSync(t *testing.T) {
	cloud := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("unexpected request")
	}))
	t.Cleanup(cloud.Close)

	s := newTestServer(t)
	s.Config.NibletCloudURL = cloud.URL
	require.NoError(t, s.setNibletCloudEnabled(false))

	require.NoError(t, s.syncNiblet(context.Background()))
	assert.Empty(t, s.installCounts)
}

func TestDisablingNibletCloudWithdrawsUsageStats(t *testing.T) {
	s, cloud := newUsageTest(t)
	require.NoError(t, s.setSetting(settingUsageSharing, "true"))
	require.NoError(t, s.setSetting(settingUsageToken, testReporterToken))
	require.NoError(t, s.syncUsage(context.Background()))
	cloud.requests = nil

	require.NoError(t, s.setNibletCloudEnabled(false))

	assert.Contains(t, cloud.requests, "DELETE /v1/tronbyt/reporters/self")
	assert.Equal(t, "false", setting(t, s, settingUsageSharing))
}

func TestCommunityStatsSettingsHandlerRequiresAdmin(t *testing.T) {
	s, _ := newUsageTest(t)
	request := httptest.NewRequest(http.MethodPost, "/settings/admin/usage-sharing", strings.NewReader("community_stats_settings=1&enabled=false"))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request = request.WithContext(context.WithValue(request.Context(), userContextKey, &data.User{Username: "member"}))
	response := httptest.NewRecorder()
	s.handleUsageSharingConsent(response, request)
	assert.Equal(t, http.StatusForbidden, response.Code)
}

func TestNibletCloudDefaultsEnabledWhenConfigured(t *testing.T) {
	s := newTestServer(t)
	s.Config.NibletCloudURL = "https://cloud.example.com"
	s.loadNibletCloudEnabled()
	require.True(t, s.isNibletCloudEnabled())
}

func TestNibletCloudEnabledPersistsAcrossRestart(t *testing.T) {
	s := newTestServer(t)
	s.Config.NibletCloudURL = "https://cloud.example.com"
	require.NoError(t, s.setNibletCloudEnabled(false))

	cfg := s.Config
	restarted := NewServer(s.DB, cfg)
	require.False(t, restarted.isNibletCloudEnabled())
}
