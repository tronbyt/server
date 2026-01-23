//go:build !windows

package main

import (
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
)

func main() {
	uid := 1000
	gid := 1000

	if s := os.Getenv("PUID"); s != "" {
		if i, err := strconv.Atoi(s); err != nil {
			slog.Warn("Invalid PUID value, using default", "puid", s, "error", err)
		} else {
			uid = i
		}
	}
	if s := os.Getenv("PGID"); s != "" {
		if i, err := strconv.Atoi(s); err != nil {
			slog.Warn("Invalid PGID value, using default", "pgid", s, "error", err)
		} else {
			gid = i
		}
	}

	dirs := []string{"data", "users"}

	if os.Getuid() == 0 {
		// Ensure data directory exists unconditionally
		if err := os.MkdirAll("data", 0755); err != nil {
			slog.Warn("Failed to create data directory", "error", err)
		}

		for _, dir := range dirs {
			// Check if the directory exists.
			// The 'data' dir is created above. 'users' is for legacy compatibility.
			info, err := os.Stat(dir)
			if os.IsNotExist(err) {
				if dir == "users" {
					slog.Debug("Users directory not found (legacy compatibility, skipping chown)", "dir", dir)
					continue
				}
				slog.Warn("Directory not found", "dir", dir, "error", err)
				continue
			}
			if err != nil {
				slog.Warn("Failed to stat directory", "dir", dir, "error", err)
				continue
			}
			if !info.IsDir() {
				slog.Warn("Path is not a directory", "path", dir)
				continue
			}
			// Recursive chown
			err = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
				if err != nil {
					slog.Warn("Cannot access path", "path", path, "error", err)
					return nil
				}

				return os.Chown(path, uid, gid)
			})
			if err != nil {
				slog.Warn("Failed to fix permissions", "dir", dir, "error", err)
			}
		}

		// Drop privileges
		if err := syscall.Setgroups([]int{gid}); err != nil {
			slog.Error("Failed to set groups", "error", err)
			os.Exit(1)
		}
		if err := syscall.Setgid(gid); err != nil {
			slog.Error("Failed to set gid", "error", err)
			os.Exit(1)
		}
		if err := syscall.Setuid(uid); err != nil {
			slog.Error("Failed to set uid", "error", err)
			os.Exit(1)
		}
	}

	if len(os.Args) < 2 {
		slog.Error("No command provided to boot wrapper")
		os.Exit(1)
	}

	cmdName := os.Args[1]
	cmdArgs := os.Args[1:]

	cmdPath, err := exec.LookPath(cmdName)
	if err != nil {
		slog.Error("Command not found", "cmd", cmdName, "error", err)
		os.Exit(1)
	}

	if err := syscall.Exec(cmdPath, cmdArgs, os.Environ()); err != nil {
		slog.Error("Failed to exec command", "cmd", cmdName, "error", err)
		os.Exit(1)
	}
}
