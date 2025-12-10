package server

import (
	"strings"
	"time"

	"tronbyt-server/internal/data"
)

// GetNightModeIsActive checks if night mode is currently active for a device.
func GetNightModeIsActive(device *data.Device) bool {
	if !device.NightModeEnabled {
		return false
	}

	// Get Device Timezone
	loc := time.Local
	if device.Timezone != nil {
		if l, err := time.LoadLocation(*device.Timezone); err == nil {
			loc = l
		}
	} else if device.Location.Timezone != "" {
		if l, err := time.LoadLocation(device.Location.Timezone); err == nil {
			loc = l
		}
	}

	currentTime := time.Now().In(loc)
	currentHM := currentTime.Format("15:04")

	start := "22:00"
	if device.NightStart != "" {
		start = device.NightStart
	}
	end := "06:00"
	if device.NightEnd != "" {
		end = device.NightEnd
	}

	if start > end {
		return currentHM >= start || currentHM <= end
	}
	return currentHM >= start && currentHM <= end
}

// GetDimModeIsActive checks if dim mode is active (dimming without full night mode).
func GetDimModeIsActive(device *data.Device) bool {
	dimTime := device.DimTime
	if dimTime == nil || *dimTime == "" {
		return false
	}

	// Get Device Timezone
	loc := time.Local
	if device.Timezone != nil {
		if l, err := time.LoadLocation(*device.Timezone); err == nil {
			loc = l
		}
	} else if device.Location.Timezone != "" {
		if l, err := time.LoadLocation(device.Location.Timezone); err == nil {
			loc = l
		}
	}

	currentTime := time.Now().In(loc)
	currentHM := currentTime.Format("15:04")

	start := *dimTime
	end := "06:00" // Default
	if device.NightEnd != "" {
		end = device.NightEnd
	}

	if start > end {
		return currentHM >= start || currentHM <= end
	}
	return currentHM >= start && currentHM <= end
}

// IsAppScheduleActive checks if an app's schedule is active.
func IsAppScheduleActive(app *data.App, device *data.Device) bool {
	// 1. Get Device Timezone
	loc := time.Local // Default
	if device.Timezone != nil {
		if l, err := time.LoadLocation(*device.Timezone); err == nil {
			loc = l
		}
	} else if device.Location.Timezone != "" {
		// Try to get timezone from location (stored in JSON)
		if l, err := time.LoadLocation(device.Location.Timezone); err == nil {
			loc = l
		}
	}

	currentTime := time.Now().In(loc)
	return IsAppScheduleActiveAtTime(app, currentTime)
}

// IsAppScheduleActiveAtTime checks if app should be active at the given time.
func IsAppScheduleActiveAtTime(app *data.App, currentTime time.Time) bool {
	// Check Time Range
	// StartTime/EndTime are strings "HH:MM"
	startStr := "00:00"
	if app.StartTime != nil {
		startStr = *app.StartTime
	}
	endStr := "23:59"
	if app.EndTime != nil {
		endStr = *app.EndTime
	}

	currentHM := currentTime.Format("15:04") // HH:MM

	inTimeRange := false
	if startStr > endStr {
		// e.g. 22:00 to 06:00
		inTimeRange = currentHM >= startStr || currentHM <= endStr
	} else {
		inTimeRange = currentHM >= startStr && currentHM <= endStr
	}

	if !inTimeRange {
		return false
	}

	// Custom Recurrence
	if app.UseCustomRecurrence && app.RecurrenceType != "" {
		return isRecurrenceActiveAtTime(app, currentTime)
	}

	// Legacy Daily Schedule
	currentDay := strings.ToLower(currentTime.Weekday().String())

	// If Days is empty, all days are active
	if len(app.Days) == 0 {
		return true
	}

	for _, day := range app.Days {
		if strings.ToLower(day) == currentDay {
			return true
		}
	}

	return false
}

func isRecurrenceActiveAtTime(app *data.App, currentTime time.Time) bool {
	// Parse Start Date
	var startDate time.Time
	if app.RecurrenceStartDate != nil && *app.RecurrenceStartDate != "" {
		if t, err := time.Parse("2006-01-02", *app.RecurrenceStartDate); err == nil {
			startDate = t
		}
	}
	if startDate.IsZero() {
		// Default to 2025-01-01 if missing (matching Python logic)
		startDate = time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	}

	// Check End Date
	currentDate := currentTime.Truncate(24 * time.Hour) // normalize to date
	if app.RecurrenceEndDate != nil && *app.RecurrenceEndDate != "" {
		if endDate, err := time.Parse("2006-01-02", *app.RecurrenceEndDate); err == nil {
			if currentDate.After(endDate) {
				return false
			}
		}
	}

	recurrenceInterval := app.RecurrenceInterval
	if recurrenceInterval <= 0 {
		recurrenceInterval = 1
	}

	switch app.RecurrenceType {
	case data.RecurrenceDaily:
		daysSince := int(currentDate.Sub(startDate).Hours() / 24)
		return daysSince >= 0 && daysSince%recurrenceInterval == 0

	case data.RecurrenceWeekly:
		daysSince := int(currentDate.Sub(startDate).Hours() / 24)
		weeksSince := daysSince / 7
		if weeksSince < 0 || weeksSince%recurrenceInterval != 0 {
			return false
		}

		// Check Weekday
		currentWeekday := strings.ToLower(currentTime.Weekday().String())

		var activeDays []string
		if weekdays, ok := app.RecurrencePattern["weekdays"].([]any); ok {
			for _, d := range weekdays {
				if s, ok := d.(string); ok {
					activeDays = append(activeDays, s)
				}
			}
		}

		// If no weekdays specified in pattern, assume all
		if len(activeDays) == 0 {
			return true
		}

		for _, day := range activeDays {
			if strings.ToLower(day) == currentWeekday {
				return true
			}
		}
		return false

	case data.RecurrenceMonthly:
		monthsSince := monthsBetween(startDate, currentDate)
		if monthsSince < 0 || monthsSince%recurrenceInterval != 0 {
			return false
		}

		// Check Pattern
		if dayOfMonth, ok := app.RecurrencePattern["day_of_month"].(float64); ok {
			return currentTime.Day() == int(dayOfMonth)
		} else if dayOfWeekPattern, ok := app.RecurrencePattern["day_of_week"].(string); ok {
			return matchesMonthlyWeekdayPattern(currentTime, dayOfWeekPattern)
		}

		return true // No specific pattern, just "every X months" -> matches any day in that month?
		// Python implementation returns True if no pattern.

	case data.RecurrenceYearly:
		yearsSince := currentDate.Year() - startDate.Year()
		if yearsSince < 0 || yearsSince%recurrenceInterval != 0 {
			return false
		}
		return currentDate.Month() == startDate.Month() && currentDate.Day() == startDate.Day()
	}

	return false
}

func monthsBetween(start, end time.Time) int {
	return (end.Year()-start.Year())*12 + int(end.Month()-start.Month())
}

func matchesMonthlyWeekdayPattern(date time.Time, pattern string) bool {
	parts := strings.Split(pattern, "_")
	if len(parts) != 2 {
		return false
	}
	occurrence, weekday := parts[0], parts[1]

	targetWeekday := strings.ToLower(weekday)
	currentWeekday := strings.ToLower(date.Weekday().String())

	if targetWeekday != currentWeekday {
		return false
	}

	// Check occurrence
	// e.g. "first", "second", "third", "fourth", "last"

	day := date.Day()

	if occurrence == "last" {
		// Add 7 days, if month changes, it was the last one
		nextWeek := date.AddDate(0, 0, 7)
		return nextWeek.Month() != date.Month()
	}

	// 1st: 1-7, 2nd: 8-14, 3rd: 15-21, 4th: 22-28
	nth := (day-1)/7 + 1

	switch occurrence {
	case "first":
		return nth == 1
	case "second":
		return nth == 2
	case "third":
		return nth == 3
	case "fourth":
		return nth == 4
	}

	return false
}
