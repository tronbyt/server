package server

import (
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

type RenderMetrics struct {
	activeCount atomic.Int64
	queuedCount atomic.Int64
	totalCount  atomic.Int64
	failedCount atomic.Int64
	totalDur    int64 // nanoseconds
	maxDur      int64
}

var renderMetrics RenderMetrics

type WebPMetrics struct {
	servedCount   atomic.Int64
	renderCount   atomic.Int64
	bytesServed   atomic.Int64
	uniqueMu      sync.Mutex
	uniqueDevices map[string]bool
}

var webpMetrics WebPMetrics

func (m *RenderMetrics) StartRender() {
	m.activeCount.Add(1)
	m.queuedCount.Add(1)
}

func (m *RenderMetrics) EndRender(dur time.Duration, failed bool) {
	m.activeCount.Add(-1)
	m.queuedCount.Add(-1)
	m.totalCount.Add(1)
	atomic.AddInt64(&m.totalDur, int64(dur))

	currentMax := atomic.LoadInt64(&m.maxDur)
	if int64(dur) > currentMax {
		atomic.StoreInt64(&m.maxDur, int64(dur))
	}

	if failed {
		m.failedCount.Add(1)
	}
}

func (m *RenderMetrics) LogStats() {
	slog.Info("Render stats",
		"active", m.activeCount.Load(),
		"queued", m.queuedCount.Load(),
		"total", m.totalCount.Load(),
		"failed", m.failedCount.Load(),
	)
}

func (m *RenderMetrics) ActiveCount() int64 {
	return m.activeCount.Load()
}

func (m *RenderMetrics) AvgDuration() time.Duration {
	total := m.totalCount.Load()
	if total == 0 {
		return 0
	}
	return time.Duration(m.totalDur / total)
}

func (m *RenderMetrics) MaxDuration() time.Duration {
	return time.Duration(atomic.LoadInt64(&m.maxDur))
}

func (w *WebPMetrics) RecordWebPServed(bytes int) {
	w.servedCount.Add(1)
	w.bytesServed.Add(int64(bytes))
}

func (w *WebPMetrics) RecordRender() {
	w.renderCount.Add(1)
}

func (w *WebPMetrics) RecordUniqueDevice(deviceID string) {
	w.uniqueMu.Lock()
	defer w.uniqueMu.Unlock()
	if w.uniqueDevices == nil {
		w.uniqueDevices = make(map[string]bool)
	}
	w.uniqueDevices[deviceID] = true
}

func (w *WebPMetrics) LogStats() {
	served := w.servedCount.Swap(0)
	renders := w.renderCount.Swap(0)
	bytes := w.bytesServed.Swap(0)

	w.uniqueMu.Lock()
	uniqueDevs := len(w.uniqueDevices)
	w.uniqueDevices = make(map[string]bool) // Reset for next window
	w.uniqueMu.Unlock()

	mbServed := float64(bytes) / (1024 * 1024)

	slog.Info("Stats 10s -- ",
		"webp_served", served,
		"renders", renders,
		"unique_devices", uniqueDevs,
		"mb_served", mbServed,
	)
}
