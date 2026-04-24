package serve

import (
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"time"
	"tronbyt-server/internal/config"
	"tronbyt-server/internal/server"

	"github.com/quic-go/quic-go/http3"
)

func serve(cfg *config.Settings, srv *server.Server) error {
	// Configure Listeners
	var listeners []net.Listener

	// TCP
	if cfg.Host != "" || cfg.Port != "" {
		addr := net.JoinHostPort(cfg.Host, cfg.Port)
		l, err := net.Listen("tcp", addr)
		if err != nil {
			return fmt.Errorf("failed to listen on TCP %s: %w", addr, err)
		}
		listeners = append(listeners, l)
		slog.Info("Listening on TCP", "addr", addr)
	}

	// Unix Socket
	if cfg.UnixSocket != "" {
		if err := os.RemoveAll(cfg.UnixSocket); err != nil {
			slog.Warn("Failed to remove old socket", "error", err)
		}
		l, err := net.Listen("unix", cfg.UnixSocket)
		if err != nil {
			return fmt.Errorf("failed to listen on Unix socket %s: %w", cfg.UnixSocket, err)
		}
		if err := os.Chmod(cfg.UnixSocket, 0666); err != nil {
			slog.Warn("Failed to set socket permissions", "error", err)
		}
		listeners = append(listeners, l)
		slog.Info("Listening on Unix socket", "path", cfg.UnixSocket)
	}

	if len(listeners) == 0 {
		return fmt.Errorf("no listeners configured")
	}

	var handler http.Handler = srv

	// Determine number of servers to start and create error channel
	numServers := len(listeners)
	http3Enabled := cfg.SSLCertFile != "" && cfg.SSLKeyFile != "" && (cfg.Host != "" || cfg.Port != "")
	if http3Enabled {
		numServers++
	}
	errCh := make(chan error, numServers)

	// TLS Config (Shared)
	var tlsConfig *tls.Config
	if cfg.SSLCertFile != "" && cfg.SSLKeyFile != "" {
		cert, err := tls.LoadX509KeyPair(cfg.SSLCertFile, cfg.SSLKeyFile)
		if err != nil {
			return fmt.Errorf("failed to load TLS certificates: %w", err)
		}
		tlsConfig = &tls.Config{
			Certificates: []tls.Certificate{cert},
			NextProtos:   []string{"h2", "http/1.1"},
		}
		slog.Info("TLS certificates loaded", "cert", cfg.SSLCertFile, "key", cfg.SSLKeyFile)
	}

	// HTTP/3 (QUIC) Support
	if http3Enabled {
		if tlsConfig == nil {
			// This should practically not happen due to http3Enabled check above,
			// but good for safety if logic changes.
			return fmt.Errorf("HTTP/3 enabled but TLS config is nil")
		}
		addr := net.JoinHostPort(cfg.Host, cfg.Port)
		h3Srv := &http3.Server{
			Addr:        addr,
			Handler:     srv,
			IdleTimeout: 120 * time.Second,
			TLSConfig:   tlsConfig,
		}

		go func() {
			slog.Info("Serving HTTP/3 (QUIC)", "addr", addr)
			err := h3Srv.ListenAndServe()
			if err != nil && err != http.ErrServerClosed {
				errCh <- fmt.Errorf("HTTP/3 server failed: %w", err)
			}
		}()

		// Wrap handler to set Alt-Svc header for TCP connections
		baseHandler := handler
		handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Determine the external port to advertise in Alt-Svc.
			// Use GetBaseURL to handle X-Forwarded-Host/Proto respecting logic centrally.
			baseURL := srv.GetBaseURL(r)
			u, err := url.Parse(baseURL)
			var port string
			if err == nil {
				port = u.Port()
			}

			if port == "" {
				// No port in URL, use default based on scheme
				// HTTP/3 implies HTTPS, so 443.
				port = "443"
			}

			w.Header().Set("Alt-Svc", fmt.Sprintf(`h3=":%s"; ma=2592000`, port))
			baseHandler.ServeHTTP(w, r)
		})
	}

	httpSrv := &http.Server{
		Handler:      handler,
		ReadTimeout:  25 * time.Second,
		WriteTimeout: 25 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	slog.Info("Tronbyt Server starting", "db", cfg.DBDSN, "dataDir", cfg.DataDir)

	for _, l := range listeners {
		go func(l net.Listener) {
			var err error
			if tlsConfig != nil {
				// Wrap listener with TLS if config is present
				l = tls.NewListener(l, tlsConfig)
			}
			err = httpSrv.Serve(l)
			if err != nil && err != http.ErrServerClosed {
				errCh <- err
			}
		}(l)
	}

	if err := <-errCh; err != nil {
		return fmt.Errorf("server failed: %w", err)
	}
	return nil
}
