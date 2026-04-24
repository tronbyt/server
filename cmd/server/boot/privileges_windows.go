//go:build windows

package boot

func dropPrivileges(uid, gid int) error {
	// No-op on Windows - privilege management works differently
	return nil
}
