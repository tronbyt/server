package server

import (
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path"
	"strings"

	"tronbyt-server/internal/data"
)

// imageNameReplacer flattens user-controlled values (app names, inames and
// pushed installation IDs) into a single filename component. The device image
// root already confines access to the device's directory; this only keeps a
// name like "AC/DC" from turning into a subdirectory.
var imageNameReplacer = strings.NewReplacer("/", "_", `\`, "_", "\x00", "_")

// openDeviceImageRoot opens the device's webp directory as an os.Root.
// App names and inames come from user input, including imported configs, so
// every file built from them is accessed through this root to keep it inside
// the device's own directory.
func (s *Server) openDeviceImageRoot(deviceID string) (*os.Root, error) {
	dir, err := s.ensureDeviceImageDir(deviceID)
	if err != nil {
		return nil, err
	}
	return os.OpenRoot(dir)
}

// readDeviceImage reads name from the device's image root.
func (s *Server) readDeviceImage(deviceID, name string) ([]byte, error) {
	root, err := s.openDeviceImageRoot(deviceID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	return root.ReadFile(name)
}

// writeDeviceImage writes name into the device's image root, creating its parent directory.
func (s *Server) writeDeviceImage(deviceID, name string, b []byte) error {
	root, err := s.openDeviceImageRoot(deviceID)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	if err := root.MkdirAll(path.Dir(name), 0755); err != nil {
		return err
	}
	return root.WriteFile(name, b, 0644)
}

// serveDeviceImage serves name from the device's image root, reporting whether it exists.
func (s *Server) serveDeviceImage(w http.ResponseWriter, r *http.Request, deviceID, name string) bool {
	root, err := s.openDeviceImageRoot(deviceID)
	if err != nil {
		slog.Error("Failed to open device image root", "device_id", deviceID, "error", err)
		return false
	}
	defer func() { _ = root.Close() }()

	f, err := root.Open(name)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil || info.IsDir() {
		return false
	}

	http.ServeContent(w, r, info.Name(), info.ModTime(), f)
	return true
}

// validInstallationID reports whether id can name an installation's files.
// It must already be a single filename, so flattening can't make two IDs
// collide, and must not start with "__", which marks one-off pushes.
func validInstallationID(id string) bool {
	return isSinglePathComponent(id) && !strings.ContainsRune(id, 0) && !strings.HasPrefix(id, "__")
}

// validateImportedApps rejects imported apps whose iname or pushed
// installation ID can't name files. Both are stored verbatim from the upload.
func validateImportedApps(apps []*data.App) error {
	for _, app := range apps {
		if app == nil {
			return fmt.Errorf("empty app entry")
		}
		if !validInstallationID(app.Iname) {
			return fmt.Errorf("invalid installation name %q", app.Iname)
		}
		if app.Path != nil {
			if id, ok := strings.CutPrefix(*app.Path, "pushed:"); ok && !validInstallationID(id) {
				return fmt.Errorf("invalid installation ID %q", id)
			}
		}
	}
	return nil
}

// appImageBase returns the filename stem shared by an app's renders: "<name>-<iname>".
func appImageBase(app *data.App) string {
	return imageNameReplacer.Replace(app.Name + "-" + app.Iname)
}

// appFullWebpName returns the unchunked preview animation's name within the device image root.
func appFullWebpName(app *data.App) string {
	return appImageBase(app) + "_full.webp"
}

// appChunksMetaName returns the chunk metadata file's name within the device image root.
func appChunksMetaName(app *data.App) string {
	return appImageBase(app) + "_chunks.json"
}

// pushedWebpName returns a pushed image's name within the device image root.
func pushedWebpName(installID string) string {
	return path.Join("pushed", imageNameReplacer.Replace(installID)+".webp")
}

// appWebpName returns the app's primary image name within the device image root.
func appWebpName(app *data.App) string {
	if app.Pushed && app.Path != nil && strings.HasPrefix(*app.Path, "pushed:") {
		return pushedWebpName(strings.TrimPrefix(*app.Path, "pushed:"))
	}
	return appImageBase(app) + ".webp"
}

// removeAppWebps deletes the "<name>-<iname>.webp" renders for iname from root.
// It matches on the suffix rather than globbing so that glob metacharacters in
// an imported iname can't widen the match to other installations.
func removeAppWebps(root *os.Root, iname string) {
	entries, err := fs.ReadDir(root.FS(), ".")
	if err != nil {
		slog.Error("Failed to read device webp directory", "error", err)
		return
	}
	suffix := "-" + imageNameReplacer.Replace(iname) + ".webp"
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), suffix) {
			continue
		}
		if err := root.Remove(entry.Name()); err != nil {
			slog.Error("Failed to remove app webp file", "name", entry.Name(), "error", err)
		}
	}
}

// removeAppChunkFiles deletes the "<base>_chunk_N.webp" files for app from root.
func removeAppChunkFiles(root *os.Root, app *data.App) {
	entries, err := fs.ReadDir(root.FS(), ".")
	if err != nil {
		return
	}
	prefix := appImageBase(app) + "_chunk_"
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasPrefix(entry.Name(), prefix) && strings.HasSuffix(entry.Name(), ".webp") {
			_ = root.Remove(entry.Name())
		}
	}
}
