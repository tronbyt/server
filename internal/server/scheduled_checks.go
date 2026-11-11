package server

import (
	"context"
	"log/slog"
	"math/rand/v2"
	"time"
)

// scheduledCheckInterval is how often the server checks GitHub for new
// firmware releases and, when automatic updates are on, for system app
// changes. Unchanged checks cost one small request each: an ETag-matched API
// call for firmware, and a ref listing for the app store.
const scheduledCheckInterval = time.Hour

// runEvery calls fn every interval until ctx ends. The first call waits a
// random part of the interval, so that servers started at the same time
// (after a release, say) don't all contact GitHub in the same minute.
func runEvery(ctx context.Context, interval time.Duration, fn func()) {
	initial := time.NewTimer(rand.N(interval))
	defer initial.Stop()
	select {
	case <-ctx.Done():
		return
	case <-initial.C:
	}
	fn()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			fn()
		}
	}
}

// autoUpdateFirmware checks for new firmware releases every hour, so the
// server knows about a release without waiting for a restart. Releases are
// listed with an ETag, so a check with nothing new transfers no data, and
// firmware files are only downloaded for releases the server doesn't have.
func (s *Server) autoUpdateFirmware(ctx context.Context) {
	if !s.Config.Production || !s.Config.EnableUpdateChecks {
		return
	}
	runEvery(ctx, scheduledCheckInterval, func() {
		if err := s.UpdateFirmwareBinaries(FirmwareReleasesAtStartup); err != nil {
			slog.Warn("Scheduled firmware check failed", "error", err)
		}
	})
}
