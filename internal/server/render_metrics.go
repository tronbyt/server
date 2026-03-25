package server

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
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
	maxDur      atomic.Int64

	// Sliding window tracking (timestamps of events in last 60 seconds)
	mu              sync.Mutex
	rendersByMinute []int64 // timestamps of renders
	reqsByMinute    []int64 // timestamps of requests
}

var renderMetrics RenderMetrics

type WebPMetrics struct {
	servedCount   atomic.Int64
	renderCount   atomic.Int64
	bytesServed   atomic.Int64
	uniqueMu      sync.Mutex
	uniqueDevices map[string]int64 // device ID -> last seen timestamp

	// Sliding window tracking
	mu            sync.Mutex
	webpsByMinute []int64 // timestamps of webp serves
}

var webpMetrics WebPMetrics

const windowDuration = 60 * time.Second

func (m *RenderMetrics) StartRender() {
	m.activeCount.Add(1)
	m.queuedCount.Add(1)
}

func (m *RenderMetrics) EndRender(dur time.Duration, failed bool) {
	m.activeCount.Add(-1)
	m.queuedCount.Add(-1)
	m.totalCount.Add(1)
	atomic.AddInt64(&m.totalDur, int64(dur))

	currentMax := m.maxDur.Load()
	if int64(dur) > currentMax {
		m.maxDur.Store(int64(dur))
	}

	if failed {
		m.failedCount.Add(1)
	}

	now := time.Now().Unix()
	m.mu.Lock()
	m.rendersByMinute = append(m.rendersByMinute, now)
	m.mu.Unlock()
}

func (m *RenderMetrics) RecordRequest() {
	now := time.Now().Unix()
	m.mu.Lock()
	m.reqsByMinute = append(m.reqsByMinute, now)
	m.mu.Unlock()
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
	return time.Duration(m.maxDur.Load())
}

func (m *RenderMetrics) TotalCount() int64 {
	return m.totalCount.Load()
}

func (m *RenderMetrics) FailedCount() int64 {
	return m.failedCount.Load()
}

func (m *RenderMetrics) QueuedCount() int64 {
	return m.queuedCount.Load()
}

func (m *RenderMetrics) RendersPerMin() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	cutoff := time.Now().Add(-windowDuration).Unix()
	var count int64
	for _, t := range m.rendersByMinute {
		if t >= cutoff {
			count++
		}
	}
	return count
}

func (m *RenderMetrics) ReqsPerMin() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	cutoff := time.Now().Add(-windowDuration).Unix()
	var count int64
	for _, t := range m.reqsByMinute {
		if t >= cutoff {
			count++
		}
	}
	return count
}

func (w *WebPMetrics) RecordWebPServed(bytes int) {
	w.servedCount.Add(1)
	w.bytesServed.Add(int64(bytes))

	now := time.Now().Unix()
	w.mu.Lock()
	w.webpsByMinute = append(w.webpsByMinute, now)
	w.mu.Unlock()
}

func (w *WebPMetrics) RecordRender() {
	w.renderCount.Add(1)
}

func (w *WebPMetrics) RecordUniqueDevice(deviceID string) {
	now := time.Now().Unix()
	w.uniqueMu.Lock()
	if w.uniqueDevices == nil {
		w.uniqueDevices = make(map[string]int64)
	}
	w.uniqueDevices[deviceID] = now
	w.uniqueMu.Unlock()
}

func (w *WebPMetrics) LogStats(renderSlotsInUse int) {
	served := w.servedCount.Swap(0)
	renders := w.renderCount.Swap(0)

	cutoff := time.Now().Add(-windowDuration).Unix()
	w.uniqueMu.Lock()
	var uniqueDevs int64
	for _, lastSeen := range w.uniqueDevices {
		if lastSeen >= cutoff {
			uniqueDevs++
		}
	}
	// Clean up old entries
	for id, lastSeen := range w.uniqueDevices {
		if lastSeen < cutoff {
			delete(w.uniqueDevices, id)
		}
	}
	w.uniqueMu.Unlock()

	loadAvg1m := getLoadAverage()
	if served > 0 {
		slog.Info(fmt.Sprintf("Stats ------ : %.1f - %d / %d - %d ", loadAvg1m, served, renders, renderSlotsInUse))
	}
}

func getLoadAverage() float64 {
	data, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return 0
	}
	parts := strings.Split(string(data), " ")
	if len(parts) < 1 {
		return 0
	}
	f, err := strconv.ParseFloat(parts[0], 64)
	if err != nil {
		return 0
	}
	return f
}

func (w *WebPMetrics) ServedCount() int64 {
	return w.servedCount.Load()
}

func (w *WebPMetrics) RenderCount() int64 {
	return w.renderCount.Load()
}

func (w *WebPMetrics) BytesServed() int64 {
	return w.bytesServed.Load()
}

func (w *WebPMetrics) WebpsPerMin() int64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	cutoff := time.Now().Add(-windowDuration).Unix()
	var count int64
	for _, t := range w.webpsByMinute {
		if t >= cutoff {
			count++
		}
	}
	return count
}

func (w *WebPMetrics) UniqueDevicesPerMin() int64 {
	cutoff := time.Now().Add(-windowDuration).Unix()
	w.uniqueMu.Lock()
	defer w.uniqueMu.Unlock()
	var count int64
	for _, lastSeen := range w.uniqueDevices {
		if lastSeen >= cutoff {
			count++
		}
	}
	return count
}

type StatsSnapshot struct {
	ActiveRenders    int64
	QueuedRenders    int64
	TotalRenders     int64
	FailedRenders    int64
	AvgRenderMs      int64
	MaxRenderMs      int64
	RendersPerMin    int64
	ReqsPerMin       int64
	WebpsServed      int64
	WebpsPerMin      int64
	BytesServedMB    float64
	UniqueDevsPerMin int64
}

func GetStatsSnapshot() StatsSnapshot {
	return StatsSnapshot{
		ActiveRenders:    renderMetrics.ActiveCount(),
		QueuedRenders:    renderMetrics.QueuedCount(),
		TotalRenders:     renderMetrics.TotalCount(),
		FailedRenders:    renderMetrics.FailedCount(),
		AvgRenderMs:      renderMetrics.AvgDuration().Milliseconds(),
		MaxRenderMs:      renderMetrics.MaxDuration().Milliseconds(),
		RendersPerMin:    renderMetrics.RendersPerMin(),
		ReqsPerMin:       renderMetrics.ReqsPerMin(),
		WebpsServed:      webpMetrics.ServedCount(),
		WebpsPerMin:      webpMetrics.WebpsPerMin(),
		BytesServedMB:    float64(webpMetrics.BytesServed()) / (1024 * 1024),
		UniqueDevsPerMin: webpMetrics.UniqueDevicesPerMin(),
	}
}
