package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"tronbyt-server/internal/apps"
	"tronbyt-server/internal/data"
)

const testReporterToken = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

type fakeUsageCloud struct {
	t          *testing.T
	requests   []string
	network    string
	reportCode int
	deleteCode int
	report     map[string]any
	onVerify   func() int
	onReport   func()
}

func (f *fakeUsageCloud) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.requests = append(f.requests, r.Method+" "+r.URL.Path)
	auth := r.Header.Get("Authorization")
	switch r.Method + " " + r.URL.Path {
	case "POST /v1/tronbyt/reporters":
		assert.Empty(f.t, auth)
		var body map[string]any
		require.NoError(f.t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(f.t, map[string]any{"protocol_version": "tronbyt-app-usage.v1", "consent": true}, body)
		w.WriteHeader(http.StatusCreated)
		_, _ = fmt.Fprintf(w, `{"protocol_version":"tronbyt-app-usage.v1","reporter_token":%q,"network":%q}`, testReporterToken, f.network)
	case "POST /v1/tronbyt/reports":
		assert.Equal(f.t, "Bearer "+testReporterToken, auth)
		require.NoError(f.t, json.NewDecoder(r.Body).Decode(&f.report))
		if f.onReport != nil {
			f.onReport()
		}
		w.WriteHeader(f.reportCode)
	case "POST /v1/tronbyt/reporters/self/checkin":
		assert.Equal(f.t, "Bearer "+testReporterToken, auth)
		var body map[string]any
		require.NoError(f.t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(f.t, map[string]any{"protocol_version": "tronbyt-app-usage.v1"}, body)
		w.WriteHeader(f.reportCode)
	case "DELETE /v1/tronbyt/reporters/self":
		assert.Equal(f.t, "Bearer "+testReporterToken, auth)
		w.WriteHeader(f.deleteCode)
	case "POST /v1/tronbyt/reporters/self/hostname":
		var body map[string]string
		require.NoError(f.t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(f.t, "https://tronbyt.example.com", body["base_url"])
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"protocol_version":"tronbyt-app-usage.v1","challenge_url":"https://tronbyt.example.com/.well-known/niblet-reporter","challenge":"00112233445566778899aabbccddeeff","expires_at":"2026-10-04T12:15:00Z"}`))
	case "POST /v1/tronbyt/reporters/self/hostname/verify":
		w.WriteHeader(f.onVerify())
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func newUsageTest(t *testing.T) (*Server, *fakeUsageCloud) {
	s := newTestServer(t)
	cloud := &fakeUsageCloud{t: t, network: "residential", reportCode: http.StatusNoContent, deleteCode: http.StatusNoContent}
	server := httptest.NewServer(cloud)
	t.Cleanup(server.Close)
	s.Config.NibletCloudURL = server.URL
	s.nibletCloudEnabled.Store(true)
	require.NoError(t, s.DB.Create(&data.Device{ID: "dev1", Username: "admin", APIKey: "key-dev1"}).Error)
	return s, cloud
}

func setting(t *testing.T, s *Server, key string) string {
	value, err := s.getSetting(key)
	require.NoError(t, err)
	return value
}

func TestUsageSharingIsOffByDefault(t *testing.T) {
	s, cloud := newUsageTest(t)
	require.NoError(t, s.syncUsage(context.Background()))
	assert.Empty(t, cloud.requests, "no consent means no requests")
	assert.Equal(t, "Off", s.usageSharingStatus().Status)
}

func untilSetting(t *testing.T, s *Server, key string) time.Duration {
	due, err := time.Parse(time.RFC3339, setting(t, s, key))
	require.NoError(t, err)
	return time.Until(due)
}

func TestUsageSharingReportsChangesAndChecksInWeekly(t *testing.T) {
	s, cloud := newUsageTest(t)
	require.NoError(t, s.setSetting(settingUsageSharing, "true"))
	require.NoError(t, s.syncUsage(context.Background()))
	assert.Equal(t, []string{"POST /v1/tronbyt/reporters", "POST /v1/tronbyt/reports"}, cloud.requests)
	assert.Equal(t, map[string]any{"protocol_version": "tronbyt-app-usage.v1", "apps": []any{}, "device_count": "1"}, cloud.report)
	assert.Equal(t, testReporterToken, setting(t, s, settingUsageToken))
	assert.Equal(t, "Contributing · last shared", s.usageSharingStatus().Status)
	checkIn := untilSetting(t, s, settingUsageCheckIn)
	assert.True(t, checkIn > 7*24*time.Hour-time.Minute && checkIn <= 8*24*time.Hour, "check-in in 7 days plus jitter, got %s", checkIn)
	gap := untilSetting(t, s, settingUsageNext)
	assert.True(t, gap > 5*time.Hour && gap <= 6*time.Hour, "changed lists wait at least 6 hours, got %s", gap)

	// Unchanged: the hourly sync sends nothing until the check-in is due.
	require.NoError(t, s.setSetting(settingUsageNext, ""))
	for range 3 {
		require.NoError(t, s.syncUsage(context.Background()))
	}
	assert.Len(t, cloud.requests, 2, "an unchanged server sends nothing")

	// A due check-in carries no app data and schedules the next one.
	require.NoError(t, s.setSetting(settingUsageCheckIn, usageTimestamp(time.Now().Add(-time.Minute))))
	require.NoError(t, s.syncUsage(context.Background()))
	assert.Equal(t, "POST /v1/tronbyt/reporters/self/checkin", cloud.requests[2])
	assert.Greater(t, untilSetting(t, s, settingUsageCheckIn), 7*24*time.Hour-time.Minute)
	assert.Empty(t, setting(t, s, settingUsageNext))

	// A change is reported at the next sync, then waits out the report gap.
	require.NoError(t, s.DB.Create(&data.Device{ID: "dev2", Username: "admin", APIKey: "key-dev2"}).Error)
	require.NoError(t, s.DB.Create(&data.Device{ID: "dev3", Username: "admin", APIKey: "key-dev3"}).Error)
	require.NoError(t, s.syncUsage(context.Background()))
	assert.Equal(t, "POST /v1/tronbyt/reports", cloud.requests[3])
	assert.Equal(t, "2-5", cloud.report["device_count"])
	require.NoError(t, s.DB.Create(&data.Device{ID: "dev4", Username: "admin", APIKey: "key-dev4"}).Error)
	require.NoError(t, s.syncUsage(context.Background()))
	assert.Len(t, cloud.requests, 4, "unchanged range: nothing to send")
}

func TestUsageSharingWaitsForADevice(t *testing.T) {
	s, cloud := newUsageTest(t)
	require.NoError(t, s.DB.Where("id = ?", "dev1").Delete(&data.Device{}).Error)
	require.NoError(t, s.setSetting(settingUsageSharing, "true"))
	require.NoError(t, s.syncUsage(context.Background()))
	assert.Empty(t, cloud.requests, "a server without devices has nothing to share")
	assert.Equal(t, "Contributing · starts when you add a device", s.usageSharingStatus().Status)
	require.NoError(t, s.DB.Create(&data.Device{ID: "dev1", Username: "admin", APIKey: "key-dev1"}).Error)
	require.NoError(t, s.syncUsage(context.Background()))
	assert.Equal(t, []string{"POST /v1/tronbyt/reporters", "POST /v1/tronbyt/reports"}, cloud.requests)
}

func TestDisableNibletCloudWaitsForRunningSyncToWithdraw(t *testing.T) {
	s, cloud := newUsageTest(t)
	require.NoError(t, s.setSetting(settingUsageSharing, "true"))
	reporting := make(chan struct{})
	release := make(chan struct{})
	cloud.onReport = func() {
		close(reporting)
		<-release
	}
	go func() {
		_ = s.syncUsage(context.Background())
	}()
	<-reporting
	disableDone := make(chan error, 1)
	go func() { disableDone <- s.setNibletCloudEnabled(false) }()
	select {
	case err := <-disableDone:
		t.Fatalf("expected disable to wait for sync, got err=%v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	require.NoError(t, <-disableDone)
	assert.Contains(t, cloud.requests, "DELETE /v1/tronbyt/reporters/self")
	assert.Empty(t, setting(t, s, settingUsageToken))
	assert.False(t, s.isNibletCloudEnabled())
}

func TestUsageConsentDuringSyncDoesNotWaitAndIsApplied(t *testing.T) {
	s, cloud := newUsageTest(t)
	require.NoError(t, s.setSetting(settingUsageSharing, "true"))
	saved := make(chan error, 1)
	cloud.onReport = func() {
		cloud.onReport = nil
		// The admin turns sharing off while the first report is in flight.
		done := make(chan error, 1)
		go func() {
			done <- s.saveUsageConsent(httptest.NewRequest(http.MethodPost, "/settings/admin/usage-sharing", nil), false)
		}()
		select {
		case err := <-done:
			saved <- err
		case <-time.After(2 * time.Second):
			saved <- errors.New("saving consent waited for the running sync")
		}
	}
	require.NoError(t, s.syncUsage(context.Background()))
	require.NoError(t, <-saved)
	s.usageSyncs.Wait()
	assert.Equal(t, []string{"POST /v1/tronbyt/reporters", "POST /v1/tronbyt/reports", "DELETE /v1/tronbyt/reporters/self"}, cloud.requests,
		"the running sync reruns and withdraws despite the report gap")
	assert.Empty(t, setting(t, s, settingUsageToken))
}

func TestSetSettingsSavesAllValues(t *testing.T) {
	s := newTestServer(t)
	require.NoError(t, s.setSetting("usage_a", "old"))
	require.NoError(t, s.setSettings(map[string]string{"usage_a": "1", "usage_b": "2"}))
	assert.Equal(t, "1", setting(t, s, "usage_a"))
	assert.Equal(t, "2", setting(t, s, "usage_b"))
}

func TestUsageSharingBacksOffWithJitter(t *testing.T) {
	for failures, base := range map[int]time.Duration{1: time.Hour, 2: 2 * time.Hour, 3: 4 * time.Hour, 6: 24 * time.Hour, 20: 24 * time.Hour} {
		for range 20 {
			delay := usageRetryDelay(failures)
			assert.True(t, delay >= base && delay <= base+base/2, "failures=%d delay=%s", failures, delay)
		}
	}
	s, cloud := newUsageTest(t)
	cloud.reportCode = http.StatusServiceUnavailable
	require.NoError(t, s.setSetting(settingUsageSharing, "true"))
	for want := 1; want <= 3; want++ {
		require.NoError(t, s.setSetting(settingUsageNext, ""))
		require.Error(t, s.syncUsage(context.Background()))
		assert.Equal(t, fmt.Sprint(want), setting(t, s, settingUsageFailures))
		require.NoError(t, s.syncUsage(context.Background()))
	}
	assert.Greater(t, untilSetting(t, s, settingUsageNext), 4*time.Hour-time.Minute, "third failure waits about 4 hours")
	assert.Len(t, cloud.requests, 4, "register once, then one report per due attempt")
	cloud.reportCode = http.StatusNoContent
	require.NoError(t, s.setSetting(settingUsageNext, ""))
	require.NoError(t, s.syncUsage(context.Background()))
	assert.Equal(t, "0", setting(t, s, settingUsageFailures))
}

func TestUsageSharingRegistersAgainAfterUnauthorized(t *testing.T) {
	s, cloud := newUsageTest(t)
	require.NoError(t, s.setSetting(settingUsageSharing, "true"))
	cloud.reportCode = http.StatusUnauthorized
	require.Error(t, s.syncUsage(context.Background()))
	assert.Empty(t, setting(t, s, settingUsageToken), "a rejected token is forgotten")
	before := len(cloud.requests)
	require.NoError(t, s.syncUsage(context.Background()))
	assert.Len(t, cloud.requests, before, "the retry backoff still applies")
	cloud.reportCode = http.StatusNoContent
	require.NoError(t, s.setSetting(settingUsageNext, ""))
	require.NoError(t, s.syncUsage(context.Background()))
	assert.Equal(t, "POST /v1/tronbyt/reporters", cloud.requests[before])
	assert.Equal(t, testReporterToken, setting(t, s, settingUsageToken))
}

func TestUsageSharingRegistrationFailureIsShown(t *testing.T) {
	s := newTestServer(t)
	cloud := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer cloud.Close()
	s.Config.NibletCloudURL = cloud.URL
	s.nibletCloudEnabled.Store(true)
	require.NoError(t, s.DB.Create(&data.Device{ID: "dev1", Username: "admin", APIKey: "key-dev1"}).Error)
	require.NoError(t, s.setSetting(settingUsageSharing, "true"))
	require.Error(t, s.syncUsage(context.Background()))
	assert.Equal(t, "Couldn't reach Niblet yet · will retry automatically", s.usageSharingStatus().Status)
}

func TestUsageSharingVerifiesHostedHostname(t *testing.T) {
	s, cloud := newUsageTest(t)
	cloud.network = "hosted"
	cloud.onVerify = func() int {
		// Cloud fetches the challenge from this server while verify is in flight.
		response := httptest.NewRecorder()
		s.Router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/.well-known/niblet-reporter", nil))
		if response.Code != http.StatusOK || response.Body.String() != "00112233445566778899aabbccddeeff" {
			return http.StatusUnprocessableEntity
		}
		return http.StatusNoContent
	}
	require.NoError(t, s.setSettings(map[string]string{settingUsageSharing: "true", settingUsageBaseURL: "https://tronbyt.example.com"}))
	require.NoError(t, s.syncUsage(context.Background()))
	assert.Equal(t, []string{
		"POST /v1/tronbyt/reporters", "POST /v1/tronbyt/reporters/self/hostname",
		"POST /v1/tronbyt/reporters/self/hostname/verify", "POST /v1/tronbyt/reports",
	}, cloud.requests)
	assert.Equal(t, "tronbyt.example.com", setting(t, s, settingUsageHostname))
	response := httptest.NewRecorder()
	s.Router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/.well-known/niblet-reporter", nil))
	assert.Equal(t, http.StatusNotFound, response.Code, "the challenge is served only while verifying")
}

func TestUsageSharingWithdrawalRetriesUntilDeleted(t *testing.T) {
	s, cloud := newUsageTest(t)
	require.NoError(t, s.setSettings(map[string]string{settingUsageSharing: "false", settingUsageToken: testReporterToken}))
	cloud.deleteCode = http.StatusServiceUnavailable
	require.Error(t, s.syncUsage(context.Background()))
	assert.Equal(t, testReporterToken, setting(t, s, settingUsageToken), "keep the credential until deletion succeeds")
	assert.Equal(t, "Off · removing your stats", s.usageSharingStatus().Status)
	cloud.deleteCode = http.StatusNoContent
	require.NoError(t, s.setSetting(settingUsageNext, ""))
	require.NoError(t, s.syncUsage(context.Background()))
	assert.Equal(t, []string{"DELETE /v1/tronbyt/reporters/self", "DELETE /v1/tronbyt/reporters/self"}, cloud.requests)
	assert.Empty(t, setting(t, s, settingUsageToken))
	require.NoError(t, s.setSetting(settingUsageNext, ""))
	require.NoError(t, s.syncUsage(context.Background()))
	assert.Len(t, cloud.requests, 2, "nothing to do once withdrawn")
}

func TestUsageConsentRequiresAdminAndSameOrigin(t *testing.T) {
	s := newTestServer(t)
	request := httptest.NewRequest(http.MethodPost, "/settings/admin/usage-sharing", strings.NewReader("consent=true"))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request = request.WithContext(context.WithValue(request.Context(), userContextKey, &data.User{IsAdmin: false}))
	response := httptest.NewRecorder()
	s.handleUsageSharingConsent(response, request)
	assert.Equal(t, http.StatusForbidden, response.Code)
	request = httptest.NewRequest(http.MethodPost, "/settings/admin/usage-sharing", strings.NewReader("consent=true"))
	request.Header.Set("Sec-Fetch-Site", "cross-site")
	response = httptest.NewRecorder()
	s.Router.ServeHTTP(response, request)
	assert.Equal(t, http.StatusForbidden, response.Code)
	assert.Empty(t, setting(t, s, settingUsageSharing))
}

func TestUsageAppsReportsSystemAppsOnce(t *testing.T) {
	root := t.TempDir()
	clock := "system-apps/apps/clock"
	clockFile := filepath.Join(root, clock, "clock.star")
	weather := "system-apps/apps/weather/weather.star"
	custom := "users/me/apps/clock/clock.star"
	catalog := []apps.AppMetadata{
		{ID: "clock", FileName: "clock.star", Path: clock},
		{ID: "weather", FileName: "weather.star", Path: "system-apps/apps/weather"},
	}
	installs := []data.App{{Path: &clock}, {Path: &clockFile}, {Path: &weather}, {Path: &custom}, {}}
	assert.Equal(t, []string{"clock", "weather"}, usageApps(root, catalog, installs))

	var many []apps.AppMetadata
	var manyInstalls []data.App
	for i := range usageMaxApps + 10 {
		path := fmt.Sprintf("system-apps/apps/app%03d", i)
		many = append(many, apps.AppMetadata{ID: fmt.Sprintf("app%03d", i), Path: path})
		manyInstalls = append(manyInstalls, data.App{Path: &path})
	}
	assert.Len(t, usageApps(root, many, manyInstalls), usageMaxApps)
}

func TestUsageReportCountsOnlyEnabledInstallations(t *testing.T) {
	s := newTestServer(t)
	enabled := "system-apps/apps/clock"
	disabled := "system-apps/apps/weather"
	require.NoError(t, s.DB.Create(&data.App{Iname: "1", DeviceID: "dev1", Enabled: true, Path: &enabled}).Error)
	require.NoError(t, s.DB.Create(&data.App{Iname: "2", DeviceID: "dev1", Enabled: false, Path: &disabled}).Error)
	s.systemAppsCache = []apps.AppMetadata{
		{ID: "clock", Path: enabled},
		{ID: "weather", Path: disabled},
	}
	report, devices, err := s.usageReport(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []string{"clock"}, report["apps"])
	assert.Zero(t, devices)
}

func TestDeviceCountRangesAndPublicBaseURL(t *testing.T) {
	for devices, want := range map[int64]string{0: "1", 1: "1", 2: "2-5", 5: "2-5", 6: "6-20", 20: "6-20", 21: "21+"} {
		assert.Equal(t, want, deviceCountRange(devices))
	}
	for raw, want := range map[string]string{
		"https://Tronbyt.Example.com":      "https://tronbyt.example.com",
		"https://tronbyt.example.com:443":  "https://tronbyt.example.com",
		"http://tronbyt.example.com":       "",
		"https://tronbyt.example.com:8000": "",
		"https://192.168.1.20":             "",
		"https://localhost":                "",
		"https://tronbyt.local":            "",
	} {
		assert.Equal(t, want, publicBaseURL(raw), raw)
	}
}

func TestShouldPromptUsageOncePerServer(t *testing.T) {
	s, _ := newUsageTest(t)
	admin := &data.User{Username: "admin", IsAdmin: true}
	assert.True(t, s.shouldPromptUsage(admin), "updated servers ask an admin once")
	assert.False(t, s.shouldPromptUsage(&data.User{Username: "member"}))
	assert.False(t, s.shouldPromptUsage(nil))
	require.NoError(t, s.setSetting(settingUsageSharing, "true"))
	assert.False(t, s.shouldPromptUsage(admin), "already sharing from System Settings")
	require.NoError(t, s.setSettings(map[string]string{settingUsageSharing: "", settingUsagePrompted: "true"}))
	assert.False(t, s.shouldPromptUsage(admin), "a choice was already made")
	s.Config.NibletCloudURL = ""
	require.NoError(t, s.setSetting(settingUsagePrompted, ""))
	assert.False(t, s.shouldPromptUsage(admin), "no prompt when Niblet is disabled")
}

func TestUsagePromptRendersUntilAnswered(t *testing.T) {
	s, _ := newUsageTest(t)
	admin := &data.User{Username: "admin", IsAdmin: true}
	render := func() string {
		response := httptest.NewRecorder()
		s.renderTemplate(response, httptest.NewRequest(http.MethodGet, "/", nil), "register", TemplateData{User: admin, UserCount: 1})
		return response.Body.String()
	}
	assert.Contains(t, render(), `id="usage-sharing-prompt"`)
	request := httptest.NewRequest(http.MethodPost, "/settings/admin/usage-sharing", strings.NewReader("consent=false&return_to=%2Fdevices%2Ftidbyt%2Faddapp"))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request = request.WithContext(context.WithValue(request.Context(), userContextKey, admin))
	response := httptest.NewRecorder()
	s.handleUsageSharingConsent(response, request)
	s.usageSyncs.Wait()
	assert.Equal(t, http.StatusSeeOther, response.Code)
	assert.Equal(t, "/devices/tidbyt/addapp", response.Header().Get("Location"))
	assert.Equal(t, "false", setting(t, s, settingUsageSharing))
	assert.Equal(t, "true", setting(t, s, settingUsagePrompted))
	assert.NotContains(t, render(), `id="usage-sharing-prompt"`, "Not now is remembered")
}

func TestLocalReturnPath(t *testing.T) {
	for raw, want := range map[string]string{
		"/devices/tidbyt/addapp?x=1": "/devices/tidbyt/addapp?x=1",
		"//evil.example.com":         "/settings/admin",
		"/\\evil.example.com":        "/settings/admin",
		"https://evil.example.com":   "/settings/admin",
		"":                           "/settings/admin",
		"/ok\r\nSet-Cookie: x":       "/settings/admin",
	} {
		assert.Equal(t, want, localReturnPath(raw), raw)
	}
}

func TestFirstAdminSetupRecordsSharingChoice(t *testing.T) {
	for _, choice := range []string{"true", ""} {
		t.Run("sharing="+choice, func(t *testing.T) {
			s, _ := newUsageTest(t)
			form := "username=admin&password=correct-horse-battery"
			if choice != "" {
				form += "&usage_sharing=" + choice
			}
			request := httptest.NewRequest(http.MethodPost, "/auth/register", strings.NewReader(form))
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			s.handleRegisterPost(httptest.NewRecorder(), request)
			s.usageSyncs.Wait()
			want := "false"
			if choice == "true" {
				want = "true"
			}
			assert.Equal(t, want, setting(t, s, settingUsageSharing))
			assert.Equal(t, "true", setting(t, s, settingUsagePrompted), "new servers never see the update prompt")
		})
	}
}
