package serve

import (
	"crypto/tls"
	"log/slog"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// certWatcher manages hot-reloading of TLS certificates by watching the
// certificate and key files for changes using fsnotify.
type certWatcher struct {
	mu       sync.RWMutex
	cert     *tls.Certificate
	certFile string
	keyFile  string
}

// newCertWatcher creates a certWatcher and performs the initial certificate load.
func newCertWatcher(certFile, keyFile string) (*certWatcher, error) {
	cw := &certWatcher{
		certFile: certFile,
		keyFile:  keyFile,
	}
	if err := cw.reload(); err != nil {
		return nil, err
	}
	return cw, nil
}

// reload loads the certificate from disk and swaps it under the write lock.
func (cw *certWatcher) reload() error {
	cert, err := tls.LoadX509KeyPair(cw.certFile, cw.keyFile)
	if err != nil {
		return err
	}
	cw.mu.Lock()
	cw.cert = &cert
	cw.mu.Unlock()
	slog.Info("TLS certificate reloaded", "cert", cw.certFile, "key", cw.keyFile)
	return nil
}

// getCertificate matches the tls.Config.GetCertificate signature.
// It is called for every TLS handshake, so new connections always get the
// latest certificate.
func (cw *certWatcher) getCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	cw.mu.RLock()
	defer cw.mu.RUnlock()
	return cw.cert, nil
}

// watch starts a background goroutine that watches the certificate and key
// files for changes using fsnotify. Changes are debounced to avoid reloading
// while both files are still being written (e.g., certbot renewals or atomic
// editor saves).
func (cw *certWatcher) watch() {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		slog.Error("Failed to create TLS cert file watcher", "error", err)
		return
	}

	// Watch the specific files for direct writes.
	for _, f := range []string{cw.certFile, cw.keyFile} {
		if err := watcher.Add(f); err != nil {
			slog.Error("Failed to watch TLS file", "error", err, "file", f)
			if closeErr := watcher.Close(); closeErr != nil {
				slog.Debug("Error closing TLS file watcher", "error", closeErr)
			}
			return
		}
	}

	// Also watch parent directories to catch atomic saves (editors that
	// write a temp file and rename it over the target, like vim or sed -i).
	// When the old inode is replaced, the original file watch becomes stale;
	// by watching the directory we can detect the new file creation.
	parentDirs := make(map[string]struct{})
	for _, f := range []string{cw.certFile, cw.keyFile} {
		parentDirs[filepath.Dir(f)] = struct{}{}
	}
	for dir := range parentDirs {
		if err := watcher.Add(dir); err != nil {
			slog.Warn("Failed to watch parent directory for TLS cert changes",
				"error", err, "dir", dir)
		}
	}

	slog.Info("Watching TLS certificate files for changes",
		"cert", cw.certFile, "key", cw.keyFile)

	go cw.watchLoop(watcher)
}

// watchLoop runs the fsnotify event loop and triggers debounced reloads.
func (cw *certWatcher) watchLoop(watcher *fsnotify.Watcher) {
	const debounceDelay = 1 * time.Second
	var timer *time.Timer

	defer func() {
		if timer != nil {
			timer.Stop()
		}
		if err := watcher.Close(); err != nil {
			slog.Debug("Error closing TLS file watcher", "error", err)
		}
	}()

	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}

			// Determine if the event is for one of our files. For parent
			// directory events we check if the named file is ours.
			relevant := event.Name == cw.certFile || event.Name == cw.keyFile

			if !relevant {
				continue
			}

			// Only react to writes and creates (not chmod, etc.).
			if event.Op&(fsnotify.Write|fsnotify.Create) == 0 {
				continue
			}

			// If a file was recreated (e.g., via atomic save), re-add the
			// watch to ensure we continue monitoring it. The parent
			// directory watch catches Create events when a new inode
			// replaces the old one.
			if event.Op&fsnotify.Create != 0 {
				if err := watcher.Add(event.Name); err != nil {
					slog.Debug("Failed to re-add TLS file watch",
						"error", err, "file", event.Name)
				}
			}

			// Debounce: reset the timer on each relevant event; reload
			// only after a quiet period.
			if timer != nil {
				timer.Stop()
			}
			timer = time.AfterFunc(debounceDelay, func() {
				if err := cw.reload(); err != nil {
					slog.Error("Failed to reload TLS certificate", "error", err)
				}
			})

		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			slog.Error("TLS cert file watcher error", "error", err)
		}
	}
}
