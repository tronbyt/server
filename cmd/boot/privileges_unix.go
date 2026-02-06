//go:build !windows

package main

import (
	"log/slog"
	"os"
	"syscall"
)

func dropPrivileges(uid, gid int) {
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
