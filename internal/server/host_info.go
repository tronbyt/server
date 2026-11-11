package server

import (
	"strings"

	"tronbyt-server/internal/data"
	"tronbyt-server/internal/version"

	"github.com/tronbyt/pixlet/runtime/modules/host"
)

// hostInfo returns what apps can read through Pixlet's server.star and
// device.star modules: the server and app store versions, and the device's
// model and firmware. device may be nil (e.g. app previews
// without a device), in which case only server details are filled in.
func (s *Server) hostInfo(device *data.Device) host.Info {
	s.systemAppsCacheMutex.RLock()
	info := host.Info{
		ServerVersion:        normalizeVersion(version.Version),
		SystemAppsCommit:     s.systemAppsCommit,
		SystemAppsCommitTime: s.systemAppsCommitTime,
	}
	s.systemAppsCacheMutex.RUnlock()

	if device == nil {
		return info
	}

	info.Device = host.Device{
		Model:           device.Type.Slug(),
		FirmwareVersion: normalizeVersion(device.Info.FirmwareVersion),
		FirmwareType:    strings.ToLower(strings.TrimSpace(device.Info.FirmwareType)),
	}
	if info.Device.Model == "unknown" || info.Device.Model == "other" {
		info.Device.Model = ""
	}
	return info
}

// normalizeVersion returns a release version with a leading "v" (e.g.
// "v2.6.0"), so apps can compare server and firmware versions the same way.
// Unknown versions return "", and non-numeric ones like "dev" are kept as-is.
func normalizeVersion(v string) string {
	v = strings.TrimSpace(v)
	switch v {
	case "", "unknown":
		return ""
	}
	if v[0] >= '0' && v[0] <= '9' {
		return "v" + v
	}
	return v
}
