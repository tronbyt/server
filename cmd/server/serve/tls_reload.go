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
	mu            sync.RWMutex
	cert          *tls.Certificate
	certFile      string
	keyFile       string
	watcher       *fsnotify.Watcher
	debounceDelay time.Duration
}

// newCertWatcher creates a certWatcher and performs the initial certificate load.
func newCertWatcher(certFile, keyFile string) (*certWatcher, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	cw := &certWatcher{
		certFile:      filepath.Clean(certFile),
		keyFile:       filepath.Clean(keyFile),
		watcher:       watcher,
		debounceDelay: 1 * time.Second,
	}
	if err := cw.reload(); err != nil {
		_ = watcher.Close()
		return nil, err
	}
	return cw, nil
}

// Close stops the file watcher and releases resources.
func (cw *certWatcher) Close() error {
	cw.mu.Lock()
	defer cw.mu.Unlock()
	if cw.watcher != nil {
		err := cw.watcher.Close()
		cw.watcher = nil
		return err
	}
	return nil
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
	// Watch the specific files for direct writes.
	for _, f := range []string{cw.certFile, cw.keyFile} {
		if err := cw.watcher.Add(f); err != nil {
			slog.Error("Failed to watch TLS file", "error", err, "file", f)
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
		if err := cw.watcher.Add(dir); err != nil {
			slog.Warn("Failed to watch parent directory for TLS cert changes",
				"error", err, "dir", dir)
		}
	}

	slog.Info("Watching TLS certificate files for changes",
		"cert", cw.certFile, "key", cw.keyFile)

	go cw.watchLoop(cw.watcher)
}

// watchLoop runs the fsnotify event loop and triggers debounced reloads.
// All work (including reloads) runs on this single goroutine: a select-based
// timer ensures that only one reload can be in flight at a time, and rapid
// successive events are coalesced into a single reload after a quiet period.
func (cw *certWatcher) watchLoop(watcher *fsnotify.Watcher) {
	var timer *time.Timer
	var delayChan <-chan time.Time

	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()

	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}

			// Clean the event name to ensure consistent path comparison
			// regardless of how the OS reports the path (e.g., relative
			// segments like "./").
			cleanName := filepath.Clean(event.Name)
			relevant := cleanName == cw.certFile || cleanName == cw.keyFile

			if !relevant {
				continue
			}

			// React to writes, creates, and renames (not chmod, etc.).
			if event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename) == 0 {
				continue
			}

			// If a file was recreated (e.g., via atomic save), re-add the
			// watch to ensure we continue monitoring it. The parent
			// directory watch catches Create events when a new inode
			// replaces the old one.
			if event.Op&fsnotify.Create != 0 {
				if err := watcher.Add(cleanName); err != nil {
					slog.Debug("Failed to re-add TLS file watch",
						"error", err, "file", cleanName)
				}
			}

			// Debounce: reset the timer on each relevant event; reload
			// only after a quiet period.
			if timer != nil {
				timer.Stop()
			}
			timer = time.NewTimer(cw.debounceDelay)
			delayChan = timer.C

		case <-delayChan:
			delayChan = nil
			if err := cw.reload(); err != nil {
				slog.Error("Failed to reload TLS certificate", "error", err)
			}

		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			slog.Error("TLS cert file watcher error", "error", err)
		}
	}
}
