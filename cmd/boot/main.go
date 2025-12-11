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
		if i, err := strconv.Atoi(s); err == nil {
			uid = i
		}
	}
	if s := os.Getenv("PGID"); s != "" {
		if i, err := strconv.Atoi(s); err == nil {
			gid = i
		}
	}

	dirs := []string{"data", "users"}

	if os.Getuid() == 0 {
		for _, dir := range dirs {
			// Ensure data directory exists if it's "data"
			if dir == "data" {
				if _, err := os.Stat(dir); os.IsNotExist(err) {
					if err := os.MkdirAll(dir, 0755); err != nil {
						slog.Warn("Failed to create directory", "dir", dir, "error", err)
					}
				}
			}

			if _, err := os.Stat(dir); os.IsNotExist(err) {
				continue
			}

			// Recursive chown
			err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
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
