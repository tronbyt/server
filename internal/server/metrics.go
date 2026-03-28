package server

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"time"

	"tronbyt-server/internal/data"

	"github.com/prometheus/client_golang/prometheus"
	"gorm.io/gorm"
)

// appMetrics holds all application-specific Prometheus metrics.
type appMetrics struct {
	renderTotal      *prometheus.CounterVec
	renderDuration   prometheus.Histogram
	devicePolls      prometheus.Counter
	wsConnections    prometheus.Gauge
	httpRequestTotal *prometheus.CounterVec
	httpRequestDur   prometheus.Histogram
}

func dbCount[T any](db *gorm.DB) float64 {
	count, _ := gorm.G[T](db).Count(context.Background(), "*")
	return float64(count)
}

func (s *Server) registerMetrics() {
	m := &appMetrics{
		renderTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "tronbyt",
				Name:      "renders_total",
				Help:      "Total number of app renders.",
			},
			[]string{"status"},
		),
		renderDuration: prometheus.NewHistogram(
			prometheus.HistogramOpts{
				Namespace: "tronbyt",
				Name:      "render_duration_seconds",
				Help:      "Duration of app renders in seconds.",
				Buckets:   []float64{0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30},
			},
		),
		devicePolls: prometheus.NewCounter(
			prometheus.CounterOpts{
				Namespace: "tronbyt",
				Name:      "device_polls_total",
				Help:      "Total number of device poll requests (GET /{id}/next).",
			},
		),
		wsConnections: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Namespace: "tronbyt",
				Name:      "websocket_connections",
				Help:      "Current number of active WebSocket connections.",
			},
		),
		httpRequestTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "tronbyt",
				Name:      "http_requests_total",
				Help:      "Total number of HTTP requests.",
			},
			[]string{"method", "status"},
		),
		httpRequestDur: prometheus.NewHistogram(
			prometheus.HistogramOpts{
				Namespace: "tronbyt",
				Name:      "http_request_duration_seconds",
				Help:      "Duration of HTTP requests in seconds.",
				Buckets:   prometheus.DefBuckets,
			},
		),
	}

	users := prometheus.NewGaugeFunc(
		prometheus.GaugeOpts{
			Namespace: "tronbyt",
			Name:      "users",
			Help:      "Number of registered users.",
		},
		func() float64 { return dbCount[data.User](s.DB) },
	)
	devices := prometheus.NewGaugeFunc(
		prometheus.GaugeOpts{
			Namespace: "tronbyt",
			Name:      "devices",
			Help:      "Number of registered devices.",
		},
		func() float64 { return dbCount[data.Device](s.DB) },
	)
	devicesActive := prometheus.NewGaugeFunc(
		prometheus.GaugeOpts{
			Namespace: "tronbyt",
			Name:      "devices_active",
			Help:      "Number of devices seen in the last 10 minutes.",
		},
		func() float64 {
			cutoff := time.Now().Add(-10 * time.Minute)
			count, _ := gorm.G[data.Device](s.DB).Where("last_seen > ?", cutoff).Count(context.Background(), "*")
			return float64(count)
		},
	)
	apps := prometheus.NewGaugeFunc(
		prometheus.GaugeOpts{
			Namespace: "tronbyt",
			Name:      "apps",
			Help:      "Number of installed apps.",
		},
		func() float64 { return dbCount[data.App](s.DB) },
	)

	collectors := []prometheus.Collector{
		m.renderTotal,
		m.renderDuration,
		m.devicePolls,
		m.wsConnections,
		m.httpRequestTotal,
		m.httpRequestDur,
		users,
		devices,
		devicesActive,
		apps,
	}
	for _, c := range collectors {
		_ = s.PromRegistry.Register(c)
	}

	// Initialize label combinations so they appear in /metrics from the start.
	m.renderTotal.WithLabelValues("success")
	m.renderTotal.WithLabelValues("error")
	m.renderTotal.WithLabelValues("empty")

	s.metrics = m
}

// statusRecorder wraps http.ResponseWriter to capture the status code.
// It implements http.Hijacker so WebSocket upgrades continue to work.
type statusRecorder struct {
	http.ResponseWriter

	statusCode int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.statusCode = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hj, ok := r.ResponseWriter.(http.Hijacker); ok {
		return hj.Hijack()
	}
	return nil, nil, fmt.Errorf("underlying ResponseWriter does not implement http.Hijacker")
}

// metricsMiddleware records HTTP request count and duration.
func (s *Server) metricsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, statusCode: http.StatusOK}
		next.ServeHTTP(rec, r)
		s.metrics.httpRequestDur.Observe(time.Since(start).Seconds())
		s.metrics.httpRequestTotal.WithLabelValues(r.Method, strconv.Itoa(rec.statusCode)).Inc()
	})
}
