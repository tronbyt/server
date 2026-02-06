//go:build windows

package main

func dropPrivileges(uid, gid int) {
	// No-op on Windows - privilege management works differently
}
