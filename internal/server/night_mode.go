package server

import (
	"fmt"
	"time"

	"tronbyt-server/internal/data"
)

func deviceTimeNow(device *data.Device) time.Time {
	loc := time.Local
	if tz := device.GetTimezone(); tz != "" {
		if loaded, err := time.LoadLocation(tz); err == nil {
			loc = loaded
		}
	}
	return time.Now().In(loc)
}

func clearNightModeOverride(device *data.Device) {
	device.NightModeOverride = nil
	device.NightModeOverrideUntil = nil
}

func clearDimModeOverride(device *data.Device) {
	device.DimModeOverride = nil
	device.DimModeOverrideUntil = nil
}

func setNightModeOverride(device *data.Device, active bool) (*time.Time, error) {
	if !device.NightModeEnabled {
		return nil, fmt.Errorf("night mode is not enabled for this device")
	}

	now := deviceTimeNow(device)
	nextChange := device.GetNightModeNextChangeAt(now)
	if nextChange == nil {
		return nil, fmt.Errorf("night mode schedule is incomplete")
	}

	override := active
	device.NightModeOverride = &override
	device.NightModeOverrideUntil = nextChange
	return nextChange, nil
}

func setDimModeOverride(device *data.Device, active bool) (*time.Time, error) {
	if !device.DimModeEnabled {
		return nil, fmt.Errorf("dim mode is not enabled for this device")
	}

	now := deviceTimeNow(device)
	nextChange := device.GetDimModeNextChangeAt(now)
	if nextChange == nil {
		return nil, fmt.Errorf("dim mode schedule is incomplete")
	}

	override := active
	device.DimModeOverride = &override
	device.DimModeOverrideUntil = nextChange
	return nextChange, nil
}
