package data

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type place struct {
	lat, lng float64
	tz       string
}

var (
	boston    = place{42.3601, -71.0589, "America/New_York"}
	sydney    = place{-33.8688, 151.2093, "Australia/Sydney"}
	honolulu  = place{21.3069, -157.8583, "Pacific/Honolulu"}
	helsinki  = place{60.1699, 24.9384, "Europe/Helsinki"}
	reykjavik = place{64.1466, -21.9426, "Atlantic/Reykjavik"}
	tromso    = place{69.6492, 18.9553, "Europe/Oslo"}
)

func mustLoad(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	require.NoError(t, err)
	return loc
}

func deviceAt(p place) Device {
	return Device{Location: DeviceLocation{Lat: p.lat, Lng: p.lng, Timezone: p.tz}}
}

func utcDay(year int, month time.Month, day int) time.Time {
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

func trig(kind TriggerKind) *ScheduleTrigger { return &ScheduleTrigger{Kind: kind} }

func fixed(hhmm string) *ScheduleTrigger { return &ScheduleTrigger{Kind: TriggerTime, Time: hhmm} }

// Sun times are checked against published almanac values with a few minutes
// of tolerance, so the test doesn't grade the library against itself.
func TestTodaySunEvents(t *testing.T) {
	loc := mustLoad(t, boston.tz)
	device := Device{
		Location: DeviceLocation{Lat: boston.lat, Lng: boston.lng, Timezone: boston.tz},
	}
	day := utcDay(2026, time.June, 21)
	events, ok := device.todaySunEvents(day.Add(12 * time.Hour).In(loc))
	require.True(t, ok)
	rise, _ := sunEventFor(TriggerSunrise, boston.lat, boston.lng, day, loc)
	set, _ := sunEventFor(TriggerSunset, boston.lat, boston.lng, day, loc)
	assert.Equal(t, wallClockHHMM(rise), events.Sunrise)
	assert.Equal(t, wallClockHHMM(set), events.Sunset)
}

func TestSunEventMatchesAlmanac(t *testing.T) {
	cases := []struct {
		name string
		p    place
		date string
		kind TriggerKind
		want string // local HH:MM
	}{
		{"boston summer sunset", boston, "2026-06-21", TriggerSunset, "20:25"},
		{"boston winter sunrise", boston, "2026-12-21", TriggerSunrise, "07:11"},
		{"sydney summer sunset", sydney, "2026-12-21", TriggerSunset, "20:05"},
		// Honolulu's evening is already the next UTC day.
		{"honolulu sunset", honolulu, "2026-06-21", TriggerSunset, "19:16"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			loc := mustLoad(t, tc.p.tz)
			day, err := time.ParseInLocation("2006-01-02", tc.date, loc)
			require.NoError(t, err)
			got, _ := sunEventFor(tc.kind, tc.p.lat, tc.p.lng, utcDay(day.Year(), day.Month(), day.Day()), loc)
			want, err := time.ParseInLocation("2006-01-02 15:04", tc.date+" "+tc.want, loc)
			require.NoError(t, err)
			assert.InDelta(t, 0, got.Sub(want).Minutes(), 4, "got %s", got.Format(time.RFC3339))
		})
	}
}

// In polar regions sunrise and sunset converge rather than disappear: at
// solar noon in polar night and at solar midnight under the midnight sun.
func TestPolarSunEventsConverge(t *testing.T) {
	loc := mustLoad(t, tromso.tz)
	rise, regime := sunEventFor(TriggerSunrise, tromso.lat, tromso.lng, utcDay(2026, time.December, 15), loc)
	set, _ := sunEventFor(TriggerSunset, tromso.lat, tromso.lng, utcDay(2026, time.December, 15), loc)
	assert.Equal(t, sunNeverRises, regime)
	assert.Equal(t, rise, set)

	set, regime = sunEventFor(TriggerSunset, tromso.lat, tromso.lng, utcDay(2026, time.June, 21), loc)
	nextRise, _ := sunEventFor(TriggerSunrise, tromso.lat, tromso.lng, utcDay(2026, time.June, 22), loc)
	assert.Equal(t, sunNeverSets, regime)
	assert.InDelta(t, 0, nextRise.Sub(set).Minutes(), 2)
}

func TestDimAtSunsetWithOffset(t *testing.T) {
	loc := mustLoad(t, boston.tz)
	device := deviceAt(boston)
	device.DimModeEnabled = true
	device.DimSchedule.Start = &ScheduleTrigger{Kind: TriggerSunset, Offset: 30}
	device.NightEnd = "06:00"

	sunset, _ := sunEventFor(TriggerSunset, boston.lat, boston.lng, utcDay(2026, time.June, 21), loc)
	start := sunset.Add(30 * time.Minute)
	assert.False(t, device.GetScheduledDimModeIsActiveAt(start.Add(-time.Second)))
	assert.True(t, device.GetScheduledDimModeIsActiveAt(start))
	assert.False(t, device.GetScheduledDimModeIsActiveAt(time.Date(2026, time.June, 22, 6, 1, 0, 0, loc)))

	next := device.GetDimModeNextChangeAt(time.Date(2026, time.June, 21, 12, 0, 0, 0, loc))
	require.NotNil(t, next)
	assert.Equal(t, start, *next)

	// The same schedule follows the seasons.
	assert.True(t, device.GetScheduledDimModeIsActiveAt(time.Date(2026, time.December, 21, 17, 0, 0, 0, loc)))
	assert.False(t, device.GetScheduledDimModeIsActiveAt(time.Date(2026, time.June, 21, 17, 0, 0, 0, loc)))
}

func TestSunTriggerClamp(t *testing.T) {
	loc := mustLoad(t, boston.tz)
	device := deviceAt(boston)
	device.DimModeEnabled = true
	device.DimSchedule.Start = &ScheduleTrigger{Kind: TriggerSunset, NotBefore: "17:00", NotAfter: "20:00"}

	// A winter sunset (~16:15) is held back to 17:00, a summer one (~20:25)
	// brought forward to 20:00.
	next := device.GetDimModeNextChangeAt(time.Date(2026, time.December, 21, 12, 0, 0, 0, loc))
	require.NotNil(t, next)
	assert.Equal(t, time.Date(2026, time.December, 21, 17, 0, 0, 0, loc), *next)
	next = device.GetDimModeNextChangeAt(time.Date(2026, time.June, 21, 12, 0, 0, 0, loc))
	require.NotNil(t, next)
	assert.Equal(t, time.Date(2026, time.June, 21, 20, 0, 0, 0, loc), *next)
}

func TestSunTriggerWithoutCoordinates(t *testing.T) {
	device := Device{DimModeEnabled: true, DimSchedule: ModeSchedule{Start: trig(TriggerSunset)}}
	at := time.Date(2026, time.December, 21, 22, 0, 0, 0, time.UTC)
	assert.False(t, device.GetScheduledDimModeIsActiveAt(at))
	assert.Nil(t, device.GetDimModeNextChangeAt(at))
}

func TestDimEndIndependentOfNight(t *testing.T) {
	dimTime := "19:00"
	device := Device{NightModeEnabled: true, NightStart: "22:00", NightEnd: "06:00", DimModeEnabled: true, DimTime: &dimTime}
	early := time.Date(2026, time.April, 25, 5, 0, 0, 0, time.UTC)

	// By default dim mode ends when night mode ends, as it always has.
	assert.True(t, device.GetScheduledDimModeIsActiveAt(early))

	device.DimSchedule.End = fixed("23:00")
	assert.True(t, device.GetScheduledDimModeIsActiveAt(time.Date(2026, time.April, 24, 22, 59, 0, 0, time.UTC)))
	assert.False(t, device.GetScheduledDimModeIsActiveAt(time.Date(2026, time.April, 24, 23, 30, 0, 0, time.UTC)))
	assert.False(t, device.GetScheduledDimModeIsActiveAt(early))

	loc := mustLoad(t, boston.tz)
	device = deviceAt(boston)
	device.DimModeEnabled = true
	device.DimSchedule = ModeSchedule{Start: trig(TriggerSunset), End: trig(TriggerSunrise)}
	assert.True(t, device.GetScheduledDimModeIsActiveAt(time.Date(2026, time.December, 22, 7, 0, 0, 0, loc)))
	assert.False(t, device.GetScheduledDimModeIsActiveAt(time.Date(2026, time.December, 22, 12, 0, 0, 0, loc)))
}

func TestWeekendEnd(t *testing.T) {
	loc := mustLoad(t, boston.tz)
	device := Device{
		NightModeEnabled: true, NightStart: "22:00", NightEnd: "06:00",
		NightSchedule: ModeSchedule{WeekendEnd: fixed("08:00")},
	}
	// 2026-10-23 is a Friday.
	assert.True(t, device.GetScheduledNightModeIsActiveAt(time.Date(2026, time.October, 24, 7, 0, 0, 0, loc)), "Saturday morning")
	assert.True(t, device.GetScheduledNightModeIsActiveAt(time.Date(2026, time.October, 25, 7, 0, 0, 0, loc)), "Sunday morning")
	assert.False(t, device.GetScheduledNightModeIsActiveAt(time.Date(2026, time.October, 26, 7, 0, 0, 0, loc)), "Monday morning")

	// Dim mode that ends with night mode follows its weekend end too.
	dimTime := "20:00"
	device.DimModeEnabled, device.DimTime = true, &dimTime
	assert.True(t, device.GetScheduledDimModeIsActiveAt(time.Date(2026, time.October, 24, 7, 0, 0, 0, loc)))
}

// "Dim from sunset to 22:00" in Helsinki: in midsummer the sun sets after
// 22:00, so there is no dim that day rather than a 24-hour window.
func TestSunsetPastFixedEndSkipsTheDay(t *testing.T) {
	loc := mustLoad(t, helsinki.tz)
	device := deviceAt(helsinki)
	device.DimModeEnabled = true
	device.DimSchedule = ModeSchedule{Start: trig(TriggerSunset), End: fixed("22:00")}
	assert.False(t, device.GetScheduledDimModeIsActiveAt(time.Date(2026, time.June, 22, 12, 0, 0, 0, loc)))
	assert.True(t, device.GetScheduledDimModeIsActiveAt(time.Date(2026, time.April, 15, 21, 0, 0, 0, loc)))
}

func TestScheduleAcrossDST(t *testing.T) {
	loc := mustLoad(t, boston.tz)
	device := Device{NightModeEnabled: true, NightStart: "22:00", NightEnd: "06:00"}

	// Spring forward on 2026-03-08 and fall back on 2026-11-01: the night is
	// an hour shorter or longer, not shifted.
	next := device.GetNightModeNextChangeAt(time.Date(2026, time.March, 7, 23, 0, 0, 0, loc))
	require.NotNil(t, next)
	assert.Equal(t, time.Date(2026, time.March, 8, 6, 0, 0, 0, loc), *next)
	next = device.GetNightModeNextChangeAt(time.Date(2026, time.October, 31, 23, 0, 0, 0, loc))
	require.NotNil(t, next)
	assert.Equal(t, 8*time.Hour, next.Sub(time.Date(2026, time.October, 31, 23, 0, 0, 0, loc)))
}

func TestScheduleTriggerValidate(t *testing.T) {
	valid := []ScheduleTrigger{
		{Kind: TriggerTime, Time: "21:30"},
		{Kind: TriggerSunset, Offset: -45, NotBefore: "17:00", NotAfter: "21:00"},
		{Kind: TriggerSunset, NotBefore: "22:00", NotAfter: "00:30"}, // the same night
	}
	for _, trigger := range valid {
		assert.NoError(t, trigger.Validate(false), "%+v", trigger)
	}
	invalid := []ScheduleTrigger{
		{Kind: TriggerTime, Time: "25:00"},
		{Kind: TriggerSunset, Offset: MaxTriggerOffsetMinutes + 1},
		{Kind: TriggerSunset, NotBefore: "21:00", NotAfter: "17:00"},
		{Kind: TriggerNightEnd},
		{Kind: "moonrise"},
	}
	for _, trigger := range invalid {
		assert.Error(t, trigger.Validate(false), "%+v", trigger)
	}
	assert.NoError(t, ScheduleTrigger{Kind: TriggerNightEnd}.Validate(true))
	assert.ErrorContains(t, ModeSchedule{End: trig(TriggerNightEnd)}.Validate(false), "end:")
}

func TestModeSchedulePersists(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file::memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&Device{}))

	// Rows written before the column existed hold NULL.
	legacy := Device{ID: "legacy01", APIKey: "key-legacy"}
	require.NoError(t, db.Create(&legacy).Error)
	require.NoError(t, db.Exec("UPDATE devices SET dim_schedule = NULL WHERE id = ?", legacy.ID).Error)
	var loaded Device
	require.NoError(t, db.First(&loaded, "id = ?", legacy.ID).Error)
	assert.Equal(t, ModeSchedule{}, loaded.DimSchedule)

	device := Device{
		ID: "sunset01", APIKey: "key-sunset",
		DimSchedule:   ModeSchedule{Start: &ScheduleTrigger{Kind: TriggerSunset, Offset: 20}, End: trig(TriggerSunrise)},
		NightSchedule: ModeSchedule{WeekendEnd: fixed("08:30")},
	}
	require.NoError(t, db.Create(&device).Error)
	var reloaded Device
	require.NoError(t, db.First(&reloaded, "id = ?", device.ID).Error)
	assert.Equal(t, device.DimSchedule, reloaded.DimSchedule)
	assert.Equal(t, device.NightSchedule, reloaded.NightSchedule)
}
