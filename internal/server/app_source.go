package server

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// errAppPathForbidden is returned for app paths outside the system apps or the owner's directory.
var errAppPathForbidden = errors.New("app path not allowed")

// appSource is an app's directory constrained to an os.Root.
type appSource struct {
	Root  *os.Root
	Name  string // file within Root, or the directory's base name for directory apps
	IsDir bool
}

func (a *appSource) Close() error {
	return a.Root.Close()
}

// IsWebP reports whether the source is a static WebP image rather than a Starlark app.
func (a *appSource) IsWebP() bool {
	return !a.IsDir && strings.EqualFold(path.Ext(a.Name), ".webp")
}

// ReadFile reads the source file.
func (a *appSource) ReadFile() ([]byte, error) {
	return a.Root.ReadFile(a.Name)
}

// splitAppPath splits a stored App.Path into the DataDir-relative directory it is confined to
// and the path within it. Only "system-apps/..." and "users/<owner>/..." are allowed.
func splitAppPath(owner, appPath string) (base, rel string, err error) {
	p := path.Clean(filepath.ToSlash(appPath))
	scope, rest, _ := strings.Cut(p, "/")
	switch scope {
	case "system-apps":
		base = scope
	case "users":
		var u string
		u, rest, _ = strings.Cut(rest, "/")
		if owner == "" || u != owner {
			return "", "", errAppPathForbidden
		}
		base = path.Join(scope, owner)
	default:
		return "", "", errAppPathForbidden
	}
	if rest == "" || rest == "." {
		return "", "", errAppPathForbidden
	}
	return base, rest, nil
}

// openAppSource opens a stored App.Path for an app on a device owned by owner.
func (s *Server) openAppSource(owner, appPath string) (*appSource, error) {
	base, rel, err := splitAppPath(owner, appPath)
	if err != nil {
		return nil, err
	}

	baseRoot, err := s.openDataRoot(base)
	if err != nil {
		return nil, err
	}
	defer func() { _ = baseRoot.Close() }()

	return openAppSourceIn(baseRoot, rel)
}

// openAppFile opens a DataDir-relative file in the system apps or owner's directory.
func (s *Server) openAppFile(owner, p string) (*os.File, error) {
	base, rel, err := splitAppPath(owner, p)
	if err != nil {
		return nil, err
	}

	baseRoot, err := s.openDataRoot(base)
	if err != nil {
		return nil, err
	}
	defer func() { _ = baseRoot.Close() }()

	return baseRoot.Open(rel)
}

// openDataRoot opens a DataDir-relative directory as an os.Root.
func (s *Server) openDataRoot(dir string) (*os.Root, error) {
	dataRoot, err := os.OpenRoot(s.DataDir)
	if err != nil {
		return nil, fmt.Errorf("opening data dir: %w", err)
	}
	defer func() { _ = dataRoot.Close() }()

	root, err := dataRoot.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", dir, err)
	}
	return root, nil
}

// openAppSourcePath opens an app at a trusted filesystem path, such as a fresh upload.
func openAppSourcePath(p string) (*appSource, error) {
	dir, name := filepath.Split(filepath.Clean(p))
	parent, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", dir, err)
	}
	defer func() { _ = parent.Close() }()

	return openAppSourceIn(parent, name)
}

// openAppSourceIn opens the app at rel within base.
func openAppSourceIn(base *os.Root, rel string) (*appSource, error) {
	info, err := base.Stat(rel)
	if err != nil {
		return nil, err
	}

	dir := path.Dir(rel)
	if info.IsDir() {
		dir = rel
	}

	root, err := base.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("opening app dir: %w", err)
	}

	return &appSource{
		Root:  root,
		Name:  path.Base(rel),
		IsDir: info.IsDir(),
	}, nil
}
