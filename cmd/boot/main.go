package main

import (
	"log"
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
						log.Printf("Failed to create %s directory: %v", dir, err)
					}
				}
			}

			if _, err := os.Stat(dir); os.IsNotExist(err) {
				continue
			}

			// Recursive chown
			err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
				if err != nil {
					return nil
				}
				return os.Chown(path, uid, gid)
			})
			if err != nil {
				log.Printf("Warning: Failed to fix permissions for %s: %v", dir, err)
			}
		}

		// Drop privileges
		if err := syscall.Setgroups([]int{gid}); err != nil {
			log.Fatalf("Failed to set groups: %v", err)
		}
		if err := syscall.Setgid(gid); err != nil {
			log.Fatalf("Failed to set gid: %v", err)
		}
		if err := syscall.Setuid(uid); err != nil {
			log.Fatalf("Failed to set uid: %v", err)
		}
	}

	if len(os.Args) < 2 {
		log.Fatal("No command provided to boot wrapper")
	}

	cmdName := os.Args[1]
	cmdArgs := os.Args[1:]

	cmdPath, err := exec.LookPath(cmdName)
	if err != nil {
		log.Fatalf("Command not found '%s': %v", cmdName, err)
	}

	if err := syscall.Exec(cmdPath, cmdArgs, os.Environ()); err != nil {
		log.Fatalf("Failed to exec '%s': %v", cmdName, err)
	}
}
