package server

// Opt-in app usage sharing with Niblet Cloud (tronbyt-app-usage.v1). An admin
// consents explicitly; the server then reports which system apps are installed
// on at least one enabled device and a coarse device count, only when that
// changes, and otherwise sends a weekly check-in without app data so Cloud can
// expire servers that are gone. No usernames, device identifiers, app settings
// or custom apps are sent.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"net/url"
	"path/filepath"
	"slices"
	"strconv"
	"time"

	"gorm.io/gorm"
	"tronbyt-server/internal/apps"
	"tronbyt-server/internal/data"
)

const (
	usageProtocol = "tronbyt-app-usage.v1"
	usageMaxApps  = 150

	// A changed app list is sent at most this often; edits in between are
	// combined into one report.
	usageReportGap = 6 * time.Hour
	// Unchanged servers check in this often, plus up to usageCheckInJitter.
	usageCheckInEvery  = 7 * 24 * time.Hour
	usageCheckInJitter = 24 * time.Hour
	// Failed sends retry after usageRetryMin, doubling up to usageRetryMax,
	// plus up to 50% jitter so an outage does not end in a synchronized retry.
	usageRetryMin = time.Hour
	usageRetryMax = 24 * time.Hour

	settingUsageSharing   = "usage_sharing"
	settingUsageToken     = "usage_token"
	settingUsageNetwork   = "usage_network"
	settingUsageBaseURL   = "usage_base_url"
	settingUsageHostname  = "usage_hostname"
	settingUsageChallenge = "usage_challenge"
	settingUsageNext      = "usage_next_report"  // no network before this time
	settingUsageCheckIn   = "usage_next_checkin" // when an unchanged server checks in
	settingUsageSent      = "usage_sent_hash"    // fingerprint of the last accepted report
	settingUsageFailures  = "usage_failures"     // consecutive failed sends
	settingUsageLast      = "usage_last_report"  // last successful report or check-in
	settingUsageError     = "usage_last_error"
	settingUsagePrompted  = "usage_prompted" // an admin has made a choice once
)

// UsageSharingStatus describes community app stats for the System Settings card.
type UsageSharingStatus struct {
	Enabled    bool   // an admin chose to contribute
	Status     string // translation message ID for the status line
	LastReport string // date of the last successful report or check-in
	Hostname   string // verified public hostname, for hosted servers
	Offline    bool   // NIBLET_CLOUD_URL is empty, so withdrawal cannot reach Cloud
}

// runUsageSync checks hourly whether community app stats need to be sent.
func (s *Server) runUsageSync() {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		if err := s.syncUsage(context.Background()); err != nil {
			slog.Warn("App usage sharing deferred", "error", err)
		}
		<-ticker.C
	}
}

// forgetUsageReporter drops the credential and everything tied to it.
func (s *Server) forgetUsageReporter() error {
	return s.setSettings(map[string]string{
		settingUsageToken: "", settingUsageNetwork: "", settingUsageHostname: "", settingUsageChallenge: "",
		settingUsageSent: "", settingUsageCheckIn: "",
	})
}

// usageRetryDelay is the backoff after the given number of consecutive
// failures: usageRetryMin doubling up to usageRetryMax, plus up to 50% jitter.
func usageRetryDelay(failures int) time.Duration {
	shift := min(max(failures-1, 0), 5)
	delay := min(usageRetryMin<<shift, usageRetryMax)
	return delay + time.Duration(rand.Int64N(int64(delay/2)+1))
}

// usageFingerprint identifies a report's content, so an unchanged server sends
// nothing but its weekly check-in.
func usageFingerprint(report map[string]any) string {
	encoded, _ := json.Marshal(map[string]any{"apps": report["apps"], "device_count": report["device_count"]})
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

// usageTimestamp formats a time for the usage settings.
func usageTimestamp(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}

// syncUsage runs one sync unless another is in progress. No lock is held while
// talking to Cloud, so saving consent never waits on the network. A consent
// change made during a sync is picked up by a forced rerun when it finishes.
func (s *Server) syncUsage(ctx context.Context) error {
	for {
		if !s.usageRunning.CompareAndSwap(false, true) {
			return nil // the running sync reruns if consent changed
		}
		err := s.syncUsageOnce(ctx, s.usageForce.Swap(false))
		s.usageRunning.Store(false)
		if !s.usageForce.Load() {
			return err
		}
	}
}

// syncUsageOnce registers, verifies a hosted hostname, reports a changed app
// list, checks in, or withdraws. It runs hourly, so edits within the hour are
// sent together. The retry time is persisted before networking so restarts
// cannot flood Cloud; force (a consent change) skips the wait.
func (s *Server) syncUsageOnce(ctx context.Context, force bool) (err error) {
	settings := make(map[string]string)
	for _, key := range []string{settingUsageSharing, settingUsageToken, settingUsageNext, settingUsageCheckIn, settingUsageSent, settingUsageFailures} {
		if settings[key], err = s.getSetting(key); err != nil {
			return err
		}
	}
	consent, token := settings[settingUsageSharing], settings[settingUsageToken]
	now := time.Now()
	if due, _ := time.Parse(time.RFC3339, settings[settingUsageNext]); !force && now.Before(due) {
		return nil
	}
	if consent != "true" && token == "" {
		return nil
	}
	var report map[string]any
	fingerprint := ""
	if consent == "true" {
		var devices int64
		if report, devices, err = s.usageReport(ctx); err != nil {
			return err
		}
		if devices == 0 {
			return nil // nothing to share until a device is added
		}
		fingerprint = usageFingerprint(report)
	}
	if consent == "true" && token != "" {
		checkIn, _ := time.Parse(time.RFC3339, settings[settingUsageCheckIn])
		if fingerprint == settings[settingUsageSent] && now.Before(checkIn) {
			return nil // nothing changed and no check-in is due
		}
	}

	failures, _ := strconv.Atoi(settings[settingUsageFailures])
	if err := s.setSetting(settingUsageNext, usageTimestamp(now.Add(usageRetryDelay(failures+1)))); err != nil {
		return err
	}
	defer func() {
		message, count := "", "0"
		if err != nil {
			message, count = err.Error(), strconv.Itoa(failures+1)
		}
		_ = s.setSettings(map[string]string{settingUsageError: message, settingUsageFailures: count})
	}()

	if consent != "true" {
		// Withdrawal: delete everything shared, keeping the token until Cloud confirms.
		if _, err := s.nibletCall(ctx, http.MethodDelete, "/v1/tronbyt/reporters/self", token, nil, http.StatusNoContent); err != nil && !errors.Is(err, errNibletUnauthorized) {
			return err
		}
		if err := s.forgetUsageReporter(); err != nil {
			return err
		}
		return s.setSetting(settingUsageNext, "")
	}
	if token == "" {
		payload, err := s.nibletCall(ctx, http.MethodPost, "/v1/tronbyt/reporters", "", map[string]any{
			"protocol_version": usageProtocol, "consent": true,
		}, http.StatusCreated)
		if err != nil {
			return err
		}
		var registered struct {
			ReporterToken string `json:"reporter_token"`
			Network       string `json:"network"`
		}
		if err := json.Unmarshal(payload, &registered); err != nil || len(registered.ReporterToken) != 64 {
			return errors.New("invalid registration response")
		}
		token = registered.ReporterToken
		if err := s.setSettings(map[string]string{settingUsageToken: token, settingUsageNetwork: registered.Network, settingUsageSent: ""}); err != nil {
			return err
		}
		settings[settingUsageSent] = ""
	}
	if err := s.verifyUsageHostname(ctx, token); err != nil {
		// Hostname proof only improves how a hosted server is counted; keep reporting.
		slog.Info("App usage hostname not verified", "error", err)
	}

	next := ""
	if fingerprint != settings[settingUsageSent] {
		_, err = s.nibletCall(ctx, http.MethodPost, "/v1/tronbyt/reports", token, report, http.StatusNoContent)
		next = usageTimestamp(now.Add(usageReportGap))
	} else {
		_, err = s.nibletCall(ctx, http.MethodPost, "/v1/tronbyt/reporters/self/checkin", token,
			map[string]any{"protocol_version": usageProtocol}, http.StatusNoContent)
	}
	if err != nil {
		if errors.Is(err, errNibletUnauthorized) {
			if forgetErr := s.forgetUsageReporter(); forgetErr != nil {
				return forgetErr
			}
		}
		return err
	}
	// Spread check-ins so servers that start together do not check in together.
	jitter := time.Duration(rand.Int64N(int64(usageCheckInJitter)))
	return s.setSettings(map[string]string{
		settingUsageSent:    fingerprint,
		settingUsageLast:    usageTimestamp(now),
		settingUsageCheckIn: usageTimestamp(now.Add(usageCheckInEvery + jitter)),
		settingUsageNext:    next,
	})
}

// verifyUsageHostname proves a hosted server's public hostname once.
func (s *Server) verifyUsageHostname(ctx context.Context, token string) error {
	network, err := s.getSetting(settingUsageNetwork)
	if err != nil || network != "hosted" {
		return err
	}
	baseURL, err := s.getSetting(settingUsageBaseURL)
	if err != nil || baseURL == "" {
		return err
	}
	verified, err := s.getSetting(settingUsageHostname)
	if err != nil || verified != "" {
		return err
	}
	payload, err := s.nibletCall(ctx, http.MethodPost, "/v1/tronbyt/reporters/self/hostname", token, map[string]any{
		"protocol_version": usageProtocol, "base_url": baseURL,
	}, http.StatusCreated)
	if err != nil {
		return err
	}
	var challenge struct {
		Challenge string `json:"challenge"`
	}
	if err := json.Unmarshal(payload, &challenge); err != nil || len(challenge.Challenge) != 32 {
		return errors.New("invalid hostname challenge")
	}
	if err := s.setSetting(settingUsageChallenge, challenge.Challenge); err != nil {
		return err
	}
	defer func() { _ = s.setSetting(settingUsageChallenge, "") }()
	if _, err := s.nibletCall(ctx, http.MethodPost, "/v1/tronbyt/reporters/self/hostname/verify", token, nil, http.StatusNoContent); err != nil {
		return err
	}
	parsed, _ := url.Parse(baseURL)
	return s.setSetting(settingUsageHostname, parsed.Hostname())
}

// handleUsageChallenge serves the pending hostname challenge to Niblet Cloud.
func (s *Server) handleUsageChallenge(w http.ResponseWriter, _ *http.Request) {
	challenge, err := s.getSetting(settingUsageChallenge)
	if err != nil || challenge == "" {
		http.NotFound(w, nil)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(challenge))
}

// usageReport builds the report body and returns the number of devices.
func (s *Server) usageReport(ctx context.Context) (map[string]any, int64, error) {
	installations, err := gorm.G[data.App](s.DB).Where("enabled = ?", true).Select("path").Find(ctx)
	if err != nil {
		return nil, 0, err
	}
	devices, err := gorm.G[data.Device](s.DB).Count(ctx, "id")
	if err != nil {
		return nil, 0, err
	}
	return map[string]any{
		"protocol_version": usageProtocol,
		"apps":             usageApps(s.DataDir, s.ListSystemApps(), installations),
		"device_count":     deviceCountRange(devices),
	}, devices, nil
}

// usageApps returns the sorted system app IDs used by the given installations.
// Custom apps never match a system app path, so they are never reported.
func usageApps(dataDir string, catalog []apps.AppMetadata, installations []data.App) []string {
	canonical := func(path string) string {
		if !filepath.IsAbs(path) {
			path = filepath.Join(dataDir, path)
		}
		absolute, _ := filepath.Abs(path)
		return absolute
	}
	paths := make(map[string]string, len(catalog)*2)
	for _, app := range catalog {
		paths[canonical(app.Path)] = app.ID
		paths[canonical(filepath.Join(app.Path, app.FileName))] = app.ID
	}
	used := make(map[string]bool)
	for _, installation := range installations {
		if installation.Path == nil {
			continue
		}
		if id, ok := paths[canonical(*installation.Path)]; ok {
			used[id] = true
		}
	}
	ids := make([]string, 0, len(used))
	for id := range used {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	if len(ids) > usageMaxApps {
		ids = ids[:usageMaxApps]
	}
	return ids
}

// deviceCountRange reports devices as a coarse range. Servers without devices
// send nothing, so the smallest range is 1.
func deviceCountRange(devices int64) string {
	switch {
	case devices <= 1:
		return "1"
	case devices <= 5:
		return "2-5"
	case devices <= 20:
		return "6-20"
	default:
		return "21+"
	}
}

// saveUsageConsent records an admin's choice from setup, the first-run prompt
// or System Settings, and acts on it right away.
func (s *Server) saveUsageConsent(r *http.Request, enabled bool) error {
	if err := s.setSettings(map[string]string{
		settingUsageSharing:  map[bool]string{true: "true", false: "false"}[enabled],
		settingUsageBaseURL:  publicBaseURL(s.GetBaseURL(r)),
		settingUsagePrompted: "true",
	}); err != nil {
		return err
	}
	s.usageForce.Store(true) // act now rather than after a retry delay
	if s.Config.NibletCloudURL != "" {
		s.usageSyncs.Go(func() {
			if err := s.syncUsage(context.Background()); err != nil {
				slog.Warn("App usage sharing deferred", "error", err)
			}
		})
	}
	return nil
}

// shouldPromptUsage asks an admin once per server, after an update, whether
// to share. New servers answer during setup instead.
func (s *Server) shouldPromptUsage(user *data.User) bool {
	if user == nil || !user.IsAdmin || s.Config.NibletCloudURL == "" {
		return false
	}
	prompted, err := s.getSetting(settingUsagePrompted)
	if err != nil || prompted == "true" {
		return false
	}
	sharing, err := s.getSetting(settingUsageSharing)
	return err == nil && sharing == ""
}

// handleUsageSharingConsent saves an admin's choice from the prompt or System
// Settings and returns to a same-site page.
func (s *Server) handleUsageSharingConsent(w http.ResponseWriter, r *http.Request) {
	if user := GetUser(r); user == nil || !user.IsAdmin {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form", http.StatusBadRequest)
		return
	}
	if err := s.saveUsageConsent(r, r.PostForm.Get("consent") == "true"); err != nil {
		http.Error(w, "Could not save app usage sharing", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, localReturnPath(r.PostForm.Get("return_to")), http.StatusSeeOther)
}

// hasDevices reports whether any device is configured.
func (s *Server) hasDevices() bool {
	devices, err := gorm.G[data.Device](s.DB).Count(context.Background(), "id")
	return err == nil && devices > 0
}

// usageSharingStatus describes the current state for System Settings.
func (s *Server) usageSharingStatus() UsageSharingStatus {
	consent, _ := s.getSetting(settingUsageSharing)
	token, _ := s.getSetting(settingUsageToken)
	last, _ := s.getSetting(settingUsageLast)
	failure, _ := s.getSetting(settingUsageError)
	hostname, _ := s.getSetting(settingUsageHostname)
	status := UsageSharingStatus{Enabled: consent == "true", Hostname: hostname, Offline: s.Config.NibletCloudURL == ""}
	lastReport, _ := time.Parse(time.RFC3339, last)
	switch {
	case !status.Enabled && token != "":
		status.Status = "Off · removing your stats"
	case !status.Enabled:
		status.Status = "Off"
	case status.Offline:
		status.Status = "Paused · NIBLET_CLOUD_URL is empty"
	case token == "" && !s.hasDevices():
		status.Status = "Contributing · starts when you add a device"
	case token == "" && failure != "":
		status.Status = "Couldn't reach Niblet yet · will retry automatically"
	case token == "":
		status.Status = "Connecting to Niblet…"
	case !lastReport.IsZero():
		status.Status = "Contributing · last shared"
		status.LastReport = lastReport.Local().Format("2 Jan 2006")
	default:
		status.Status = "Contributing · first update pending"
	}
	return status
}
