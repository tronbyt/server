package server

// Install counts for system apps are fetched from Niblet Cloud's public
// app-install-counts.v1 endpoint and cached locally.

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"
)

// nibletCountResponse is the app-install-counts.v1 payload.
type nibletCountResponse struct {
	ProtocolVersion string           `json:"protocol_version"`
	Niblet          map[string]int64 `json:"niblet"`
}

// fetchNibletCounts downloads the public install counts.
func (s *Server) fetchNibletCounts(ctx context.Context) ([]byte, error) {
	return s.nibletCall(ctx, http.MethodGet, "/v1/catalog/install-counts", "", nil, http.StatusOK)
}

// runNibletSync checks hourly whether install counts need refreshing.
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

// parseNibletCounts validates an install counts payload.
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

// setNibletCounts replaces the counts shown on system app cards.
func (s *Server) setNibletCounts(counts map[string]int64) {
	s.systemAppsCacheMutex.Lock()
	s.installCounts = counts
	s.systemAppsCacheMutex.Unlock()
}

// syncNiblet refreshes counts at most every 24 hours, keeps the last good
// counts across restarts, and backs off for an hour after failures.
func (s *Server) syncNiblet(ctx context.Context) error {
	if !s.isNibletCloudEnabled() {
		return nil
	}
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
