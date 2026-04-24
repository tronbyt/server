package boot

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"

	"github.com/spf13/cobra"
)

const Name = "boot"

func New() *cobra.Command {
	cmd := &cobra.Command{
		Use:   Name,
		Short: "Boot wrapper",
		RunE:  run,

		SilenceUsage: true,
	}
	cmd.Hidden = true
	return cmd
}

func run(_ *cobra.Command, args []string) error {
	uid, gid := 1000, 1000

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

		if err := dropPrivileges(uid, gid); err != nil {
			return fmt.Errorf("failed to drop privileges: %w", err)
		}
	}

	if len(args) == 0 {
		return errors.New("no command provided to boot wrapper")
	}

	cmdName := args[0]
	cmdArgs := args[0:]

	cmdPath, err := exec.LookPath(cmdName)
	if err != nil {
		return fmt.Errorf("%s: command not found: %w", cmdName, err)
	}

	if err := syscall.Exec(cmdPath, cmdArgs, os.Environ()); err != nil {
		return fmt.Errorf("%s: exec: %w", cmdName, err)
	}
	return nil
}
