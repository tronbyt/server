package data

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// The functions below are the schedule evaluation as it was before sun-aware
// schedules, copied verbatim apart from being turned into plain functions.
// They pin the guarantee that devices with no triggers set behave exactly as
// before, including on DST transition days.

func legacyNightActiveAt(d Device, now time.Time) bool {
	if !d.NightModeEnabled {
		return false
	}

	start, end := d.getNightScheduleBounds()
	currentMinutes := now.Hour()*60 + now.Minute()
	startMinutes, startOK := clockToMinutes(start)
	endMinutes, endOK := clockToMinutes(end)
	if !startOK || !endOK {
		return false
	}

	if startMinutes > endMinutes {
		return currentMinutes >= startMinutes || currentMinutes <= endMinutes
	}
	return currentMinutes >= startMinutes && currentMinutes <= endMinutes
}
func legacyNightNextChangeAt(d Device, now time.Time) *time.Time {
	if !d.NightModeEnabled {
		return nil
	}

	start, end := d.getNightScheduleBounds()
	startParsed, err := time.Parse("15:04", start)
	if err != nil {
		return nil
	}
	endParsed, err := time.Parse("15:04", end)
	if err != nil {
		return nil
	}

	var next *time.Time
	for dayOffset := 0; dayOffset <= 2; dayOffset++ {
		day := now.AddDate(0, 0, dayOffset)
		candidates := []time.Time{
			time.Date(day.Year(), day.Month(), day.Day(), startParsed.Hour(), startParsed.Minute(), 0, 0, now.Location()),
			time.Date(day.Year(), day.Month(), day.Day(), endParsed.Hour(), endParsed.Minute(), 0, 0, now.Location()),
		}
		for _, candidate := range candidates {
			if !candidate.After(now) {
				continue
			}
			if next == nil || candidate.Before(*next) {
				candidateCopy := candidate
				next = &candidateCopy
			}
		}
	}
	return next
}
func legacyDimBounds(d Device) (string, string, bool) {
	if d.DimTime == nil || *d.DimTime == "" {
		return "", "", false
	}
	start := *d.DimTime
	end := "06:00"
	if d.NightEnd != "" {
		end = d.NightEnd
	}
	return start, end, true
}
func legacyDimActiveAt(d Device, now time.Time) bool {
	if !d.DimModeEnabled {
		return false
	}
	start, end, ok := legacyDimBounds(d)
	if !ok {
		return false
	}

	currentMinutes := now.Hour()*60 + now.Minute()
	startMinutes, startOK := clockToMinutes(start)
	endMinutes, endOK := clockToMinutes(end)
	if !startOK || !endOK {
		return false
	}

	if startMinutes > endMinutes {
		return currentMinutes >= startMinutes || currentMinutes <= endMinutes
	}
	return currentMinutes >= startMinutes && currentMinutes <= endMinutes
}

// legacyGridTimes returns the HH:MM values to try as schedule boundaries:
// malformed values, every hour, and the half and quarter hours where DST
// transitions happen somewhere.
func legacyGridTimes() []string {
	times := []string{"", "23:59", "24:00", "7:5"}
	for h := range 24 {
		times = append(times, fmt.Sprintf("%02d:00", h))
	}
	return append(times, "22:30", "23:30", "00:30", "01:30", "02:15", "02:30", "02:45")
}

// DST transition days in 2026 where gaps and repeated hours land on common
// night-mode hours: Nuuk and Cairo shift at 23:00/00:00, Santiago at
// midnight, Lord Howe by 30 minutes.
var legacyZoneDays = []struct{ zone, day string }{
	{"UTC", "2026-06-15"},
	{"America/New_York", "2026-03-08"},
	{"America/New_York", "2026-11-01"},
	{"America/Nuuk", "2026-03-28"},
	{"America/Santiago", "2026-09-05"},
	{"Africa/Cairo", "2026-10-29"},
	{"Australia/Lord_Howe", "2026-10-04"},
	{"Pacific/Chatham", "2026-09-27"},
}

// legacyInstants steps through a day and a half of real time around the
// transition, so a repeated hour is visited twice.
func legacyInstants(t *testing.T, zone, day string) []time.Time {
	t.Helper()
	date, err := time.ParseInLocation("2006-01-02", day, mustLoad(t, zone))
	if err != nil {
		t.Fatal(err)
	}
	var instants []time.Time
	for at := date.Add(-6 * time.Hour); at.Before(date.Add(30 * time.Hour)); at = at.Add(30 * time.Minute) {
		instants = append(instants, at, at.Add(59*time.Second))
	}
	return instants
}

type mismatches struct {
	count    int
	examples []string
}

func (m *mismatches) addf(format string, args ...any) {
	m.count++
	if len(m.examples) < 10 {
		m.examples = append(m.examples, fmt.Sprintf(format, args...))
	}
}

func (m *mismatches) check(t *testing.T, what string) {
	t.Helper()
	assert.Zero(t, m.count, "%s diverges from legacy:\n%s", what, strings.Join(m.examples, "\n"))
}

// Devices without the new triggers must behave exactly as before, on DST
// transition days too.
func TestLegacyScheduleIsUnchanged(t *testing.T) {
	t.Parallel()
	times := legacyGridTimes()
	for _, zd := range legacyZoneDays {
		t.Run(zd.zone+"/"+zd.day, func(t *testing.T) {
			t.Parallel()
			instants := legacyInstants(t, zd.zone, zd.day)
			var night, next, dimmed mismatches
			for _, a := range times {
				for _, b := range times {
					device := Device{NightModeEnabled: true, NightStart: a, NightEnd: b}
					dimTime := a
					dimDevice := Device{DimModeEnabled: true, DimTime: &dimTime, NightEnd: b}
					for _, at := range instants {
						if got, want := device.GetScheduledNightModeIsActiveAt(at), legacyNightActiveAt(device, at); got != want {
							night.addf("night %s-%s at %s: got %v, legacy %v", a, b, at.Format(time.RFC3339), got, want)
						}
						if got, want := dimDevice.GetScheduledDimModeIsActiveAt(at), legacyDimActiveAt(dimDevice, at); got != want {
							dimmed.addf("dim %s, night end %s at %s: got %v, legacy %v", a, b, at.Format(time.RFC3339), got, want)
						}
						// NextChangeAt is compared away from transitions only.
						// The legacy code also returned nil for loosely
						// formatted times like "7:5" that its IsActive accepted.
						if zd.zone != "UTC" || a == "7:5" || b == "7:5" {
							continue
						}
						got, want := device.GetNightModeNextChangeAt(at), legacyNightNextChangeAt(device, at)
						if (got == nil) != (want == nil) || (got != nil && !got.Equal(*want)) {
							next.addf("night %s-%s at %s: got %v, legacy %v", a, b, at.Format(time.RFC3339), got, want)
						}
					}
				}
			}
			night.check(t, "night IsActive")
			dimmed.check(t, "dim IsActive")
			next.check(t, "night NextChangeAt")
		})
	}
}
