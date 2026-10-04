package server

// Install counts for system apps are fetched from Niblet Cloud's public
// app-install-counts.v1 endpoint and cached locally.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type nibletCountResponse struct {
	ProtocolVersion string           `json:"protocol_version"`
	Niblet          map[string]int64 `json:"niblet"`
}

func (s *Server) fetchNibletCounts(ctx context.Context) ([]byte, error) {
	base, err := url.Parse(s.Config.NibletCloudURL)
	if err != nil || base == nil || base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" ||
		(base.Scheme != "https" && (base.Scheme != "http" || (base.Hostname() != "127.0.0.1" && base.Hostname() != "localhost" && base.Hostname() != "::1"))) {
		return nil, errors.New("niblet cloud requires HTTPS (HTTP is allowed only on loopback)")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(base.String(), "/")+"/v1/catalog/install-counts", nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Do(req)
	if err != nil {
		return nil, errors.New("niblet cloud request failed")
	}
	defer func() {
		if err := res.Body.Close(); err != nil {
			slog.Debug("Failed to close Niblet Cloud response", "error", err)
		}
	}()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, fmt.Errorf("niblet cloud returned HTTP %d", res.StatusCode)
	}
	payload, err := io.ReadAll(io.LimitReader(res.Body, 1024*1024+1))
	if len(payload) > 1024*1024 {
		return nil, errors.New("niblet response too large")
	}
	return payload, err
}

func (s *Server) runNibletSync() {
	// Existing server background jobs live for the process lifetime.
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		if err := s.syncNiblet(context.Background()); err != nil {
			slog.Warn("Niblet install counts sync deferred", "error", err)
		}
		<-ticker.C
	}
}

func parseNibletCounts(payload []byte) (map[string]int64, error) {
	var counts nibletCountResponse
	if err := json.Unmarshal(payload, &counts); err != nil {
		return nil, err
	}
	if counts.ProtocolVersion != "app-install-counts.v1" || counts.Niblet == nil || len(counts.Niblet) > 5000 {
		return nil, errors.New("invalid niblet count response")
	}
	for _, count := range counts.Niblet {
		if count < 0 {
			return nil, errors.New("invalid niblet count")
		}
	}
	return counts.Niblet, nil
}

func (s *Server) setNibletCounts(counts map[string]int64) {
	s.systemAppsCacheMutex.Lock()
	s.installCounts = counts
	s.systemAppsCacheMutex.Unlock()
}

// syncNiblet refreshes counts at most every 24 hours, keeps the last good
// counts across restarts, and backs off for an hour after failures.
func (s *Server) syncNiblet(ctx context.Context) error {
	cached, err := s.getSetting("niblet_counts")
	if err != nil {
		return err
	}
	if counts, err := parseNibletCounts([]byte(cached)); err == nil {
		s.setNibletCounts(counts)
	}
	next, err := s.getSetting("niblet_next_sync")
	if err != nil {
		return err
	}
	due, _ := time.Parse(time.RFC3339, next)
	if time.Now().Before(due) {
		return nil
	}
	// Persist backoff before networking so restarts cannot spam Cloud.
	if err := s.setSetting("niblet_next_sync", time.Now().Add(time.Hour).UTC().Format(time.RFC3339)); err != nil {
		return err
	}
	payload, err := s.fetchNibletCounts(ctx)
	if err != nil {
		return err
	}
	counts, err := parseNibletCounts(payload)
	if err != nil {
		return err
	}
	if err := s.setSetting("niblet_counts", string(payload)); err != nil {
		return err
	}
	s.setNibletCounts(counts)
	return s.setSetting("niblet_next_sync", time.Now().Add(24*time.Hour).UTC().Format(time.RFC3339))
}
