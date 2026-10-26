package server

// Admin preference for contacting Niblet Cloud (install counts and community stats).

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
)

const settingNibletCloudEnabled = "niblet_cloud_enabled"

// isNibletCloudConfigured reports whether NIBLET_CLOUD_URL is set.
func (s *Server) isNibletCloudConfigured() bool {
	return s.Config.NibletCloudURL != ""
}

// isNibletCloudEnabled reports whether this server may contact Niblet Cloud.
func (s *Server) isNibletCloudEnabled() bool {
	return s.isNibletCloudConfigured() && s.nibletCloudEnabled.Load()
}

func (s *Server) loadNibletCloudEnabled() {
	if !s.isNibletCloudConfigured() {
		s.nibletCloudEnabled.Store(false)
		return
	}
	val, err := s.getSetting(settingNibletCloudEnabled)
	if err != nil {
		slog.Warn("Failed to load Niblet Cloud preference", "error", err)
		s.nibletCloudEnabled.Store(false)
		return
	}
	if val == "" {
		s.nibletCloudEnabled.Store(true)
		return
	}
	s.nibletCloudEnabled.Store(val == "true")
}

// setNibletCloudEnabled saves an admin's choice and stops or resumes Niblet traffic.
func (s *Server) setNibletCloudEnabled(enabled bool) error {
	if !s.isNibletCloudConfigured() {
		return errors.New("niblet cloud is not configured")
	}
	s.nibletCloudPreferenceMutex.Lock()
	wasEnabled := s.nibletCloudEnabled.Load()
	s.nibletCloudPreferenceMutex.Unlock()

	if wasEnabled && !enabled {
		if err := s.finishUsageWithdrawal(context.Background()); err != nil {
			return err
		}
	}

	s.nibletCloudPreferenceMutex.Lock()
	defer s.nibletCloudPreferenceMutex.Unlock()

	if err := s.setSetting(settingNibletCloudEnabled, strconv.FormatBool(enabled)); err != nil {
		return err
	}
	s.nibletCloudEnabled.Store(enabled)
	if wasEnabled && !enabled {
		s.setNibletCounts(map[string]int64{})
	}

	if !wasEnabled && enabled {
		s.usageSyncs.Go(func() {
			if err := s.syncNiblet(context.Background()); err != nil {
				slog.Warn("Niblet install counts sync deferred", "error", err)
			}
		})
	}
	return nil
}
