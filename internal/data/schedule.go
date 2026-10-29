package data

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"time"

	"github.com/nathan-osman/go-sunrise"
)

// TriggerKind selects what a night or dim mode boundary is anchored to.
type TriggerKind string

const (
	// TriggerTime is a fixed local clock time (HH:MM).
	TriggerTime TriggerKind = "time"
	// TriggerSunrise is the device location's sunrise, shifted by Offset minutes.
	TriggerSunrise TriggerKind = "sunrise"
	// TriggerSunset is the device location's sunset, shifted by Offset minutes.
	TriggerSunset TriggerKind = "sunset"
	// TriggerNightEnd follows whatever ends night mode on that day. Only valid
	// as a dim mode end or weekend end; it is the default dim end (dim mode
	// historically ended at the night end time).
	TriggerNightEnd TriggerKind = "night_end"
)

// MaxTriggerOffsetMinutes bounds how far a sun trigger can be shifted.
const MaxTriggerOffsetMinutes = 240

// ScheduleTrigger is one boundary (start or end) of a night or dim schedule.
type ScheduleTrigger struct {
	Kind TriggerKind `json:"kind"`
	// Time is the HH:MM clock time for TriggerTime.
	Time string `json:"time,omitempty"`
	// Offset shifts a sun event: positive is after, negative is before.
	Offset int `json:"offset,omitempty"`
	// NotBefore and NotAfter clamp a sun trigger to a clock-time range, so
	// e.g. a 4:15 PM winter sunset can be held back to 5:00 PM. They are read
	// in the 24 hours around the event: 06:00-06:00 for sunset (so "00:30"
	// means later that night) and 18:00-18:00 for sunrise (so "23:00" means
	// the evening before).
	NotBefore string `json:"not_before,omitempty"`
	NotAfter  string `json:"not_after,omitempty"`
}

// IsSunBased reports whether the trigger needs the device's coordinates.
func (t ScheduleTrigger) IsSunBased() bool {
	return t.Kind == TriggerSunrise || t.Kind == TriggerSunset
}

// Validate checks the trigger is well formed. allowNightEnd permits
// TriggerNightEnd, which only makes sense for a dim mode end.
func (t ScheduleTrigger) Validate(allowNightEnd bool) error {
	switch t.Kind {
	case TriggerTime:
		if _, ok := clockToMinutes(t.Time); !ok {
			return fmt.Errorf("invalid time %q, expected HH:MM", t.Time)
		}
	case TriggerSunrise, TriggerSunset:
		if t.Offset < -MaxTriggerOffsetMinutes || t.Offset > MaxTriggerOffsetMinutes {
			return fmt.Errorf("offset must be between -%d and %d minutes", MaxTriggerOffsetMinutes, MaxTriggerOffsetMinutes)
		}
		notBefore, hasNotBefore := 0, t.NotBefore != ""
		if hasNotBefore {
			m, ok := clockToMinutes(t.NotBefore)
			if !ok {
				return fmt.Errorf("invalid not-before time %q, expected HH:MM", t.NotBefore)
			}
			notBefore = sunFrameMinutes(t.Kind, m)
		}
		if t.NotAfter != "" {
			m, ok := clockToMinutes(t.NotAfter)
			if !ok {
				return fmt.Errorf("invalid not-after time %q, expected HH:MM", t.NotAfter)
			}
			if hasNotBefore && notBefore > sunFrameMinutes(t.Kind, m) {
				return errors.New("not-before time must not be later than not-after time")
			}
		}
	case TriggerNightEnd:
		if !allowNightEnd {
			return errors.New("night_end is only valid as a dim mode end")
		}
	default:
		return fmt.Errorf("unknown trigger kind %q", t.Kind)
	}
	return nil
}

// ModeSchedule holds the sun-aware and independent boundaries of a night or
// dim schedule. A nil Start or End falls back to the legacy HH:MM device
// fields (NightStart/NightEnd for night mode, DimTime for the dim start; the
// dim end defaults to TriggerNightEnd), so existing devices behave exactly as
// before.
type ModeSchedule struct {
	Start *ScheduleTrigger `json:"start,omitempty"`
	End   *ScheduleTrigger `json:"end,omitempty"`
	// WeekendEnd, when set, is used instead of End on weekends (Saturday and
	// Sunday): for windows that end on a Saturday or Sunday morning (Friday
	// and Saturday nights), and for same-day windows on those days. E.g. to
	// stay dim until 8:00 on weekend mornings. See windowFor for how the end
	// and its day are chosen when End and WeekendEnd sit on different sides
	// of midnight.
	WeekendEnd *ScheduleTrigger `json:"weekend_end,omitempty"`
}

// Validate checks every trigger that is set. allowNightEnd permits a
// TriggerNightEnd end, which only dim mode can use.
func (s ModeSchedule) Validate(allowNightEnd bool) error {
	for _, part := range []struct {
		name          string
		trigger       *ScheduleTrigger
		allowNightEnd bool
	}{
		{"start", s.Start, false},
		{"end", s.End, allowNightEnd},
		{"weekend end", s.WeekendEnd, allowNightEnd},
	} {
		if part.trigger == nil {
			continue
		}
		if err := part.trigger.Validate(part.allowNightEnd); err != nil {
			return fmt.Errorf("%s: %w", part.name, err)
		}
	}
	return nil
}

// ValidateSchedules checks the device's night and dim schedules, e.g. after
// importing a device configuration.
func (d *Device) ValidateSchedules() error {
	if err := d.NightSchedule.Validate(false); err != nil {
		return fmt.Errorf("night schedule: %w", err)
	}
	if err := d.DimSchedule.Validate(true); err != nil {
		return fmt.Errorf("dim schedule: %w", err)
	}
	return nil
}

// UsesSun reports whether any trigger needs the device's coordinates.
func (s ModeSchedule) UsesSun() bool {
	for _, t := range []*ScheduleTrigger{s.Start, s.End, s.WeekendEnd} {
		if t != nil && t.IsSunBased() {
			return true
		}
	}
	return false
}

func (s ModeSchedule) Value() (driver.Value, error) {
	return json.Marshal(s)
}

func (s *ModeSchedule) Scan(value any) error {
	if value == nil {
		return nil
	}
	var bytes []byte
	switch v := value.(type) {
	case []byte:
		bytes = v
	case string:
		bytes = []byte(v)
	default:
		return errors.New("type assertion to []byte or string failed")
	}
	if len(bytes) == 0 {
		return nil
	}
	return json.Unmarshal(bytes, s)
}

// HasCoordinates reports whether the device location has usable lat/lng for
// sunrise and sunset calculations.
func (d Device) HasCoordinates() bool {
	return d.Location.Lat != 0 || d.Location.Lng != 0
}

// TodaySunEvents holds plain sunrise and sunset wall-clock times (HH:MM) for
// the device's local calendar day, for display in the settings UI.
type TodaySunEvents struct {
	Sunrise string
	Sunset  string
}

// TodaySunEvents returns today's plain sunrise and sunset at the device
// location, or false when coordinates are missing or times cannot be resolved.
func (d Device) TodaySunEvents() (TodaySunEvents, bool) {
	return d.todaySunEvents(time.Now())
}

func (d Device) todaySunEvents(now time.Time) (TodaySunEvents, bool) {
	if !d.HasCoordinates() {
		return TodaySunEvents{}, false
	}
	loc := d.GetLocation()
	day := wallMidnight(wallOf(now.In(loc)))
	rise, _, okR := d.resolveTriggerRegime(ScheduleTrigger{Kind: TriggerSunrise}, day, loc)
	set, _, okS := d.resolveTriggerRegime(ScheduleTrigger{Kind: TriggerSunset}, day, loc)
	if !okR || !okS {
		return TodaySunEvents{}, false
	}
	return TodaySunEvents{
		Sunrise: wallClockHHMM(rise),
		Sunset:  wallClockHHMM(set),
	}, true
}

func wallClockHHMM(wall time.Time) string {
	w := wallOf(wall)
	return fmt.Sprintf("%02d:%02d", w.Hour(), w.Minute())
}

// Schedules are evaluated in wall-clock time: local date and clock fields
// carried in a UTC time.Time, so a day is always 24 hours. This is the same
// space the original minutes-of-day comparison worked in, which keeps fixed
// HH:MM schedules identical to before on DST transition days (including the
// repeated hour). Real instants are only produced for NextChangeAt.
//
// Known limitation: a sun-based boundary that lands in the repeated hour of a
// fall-back night is also only a wall-clock time, so it can't tell the two
// passes of that hour apart. With a large offset in a zone that falls back in
// the evening or at 02:00 (sunset+240 in Chile, sunrise-240 in Sydney), the
// mode can switch off or on for part of that hour once a year.

// wallDay is the length of a wall-clock day, which never has a DST change.
const wallDay = 24 * time.Hour

func wallOf(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), time.UTC)
}

// instantAtWall returns the first instant at which the local clock in loc
// reaches wall: the transition itself when wall falls in a DST gap, and the
// first occurrence when it falls in the repeated hour.
func instantAtWall(wall time.Time, loc *time.Location) time.Time {
	t := time.Date(wall.Year(), wall.Month(), wall.Day(), wall.Hour(), wall.Minute(), wall.Second(), 0, loc)
	got := wallOf(t)
	switch {
	case got.After(wall): // normalised forward out of a gap
		start, _ := t.ZoneBounds()
		if !start.IsZero() {
			return start.In(loc)
		}
	case got.Before(wall): // normalised backward out of a gap
		_, end := t.ZoneBounds()
		if !end.IsZero() {
			return end.In(loc)
		}
	default:
		if start, _ := t.ZoneBounds(); !start.IsZero() {
			_, prevOffset := start.Add(-time.Second).Zone()
			earlier := wall.Add(-time.Duration(prevOffset) * time.Second).In(loc)
			if earlier.Before(t) && wallOf(earlier).Equal(wall) {
				return earlier
			}
		}
	}
	return t
}

// scheduleMode selects which of the device's schedules to evaluate.
type scheduleMode int

const (
	nightSchedule scheduleMode = iota
	dimSchedule
)

func isWeekend(day time.Weekday) bool {
	return day == time.Saturday || day == time.Sunday
}

func weekendIndex(day time.Weekday) int {
	if isWeekend(day) {
		return 1
	}
	return 0
}

// schedulePlan is a mode's triggers, looked up once per evaluation.
type schedulePlan struct {
	start ScheduleTrigger
	// ends holds the weekday (0) and weekend (1) end triggers.
	ends [2]ScheduleTrigger
	// usesSun is set if any trigger, including a night_end it follows, is
	// sun-based; otherwise the windows repeat every week.
	usesSun bool
}

// planFor returns the mode's plan, or false if it has no start.
func (d *Device) planFor(mode scheduleMode) (schedulePlan, bool) {
	start, ok := d.startTrigger(mode)
	if !ok {
		return schedulePlan{}, false
	}
	plan := schedulePlan{
		start:   start,
		ends:    [2]ScheduleTrigger{d.endTriggerOn(mode, time.Monday), d.endTriggerOn(mode, time.Saturday)},
		usesSun: start.IsSunBased(),
	}
	for i, day := range [2]time.Weekday{time.Monday, time.Saturday} {
		if end, ok := d.concreteTrigger(plan.ends[i], day); ok && end.IsSunBased() {
			plan.usesSun = true
		}
	}
	return plan, true
}

// startTrigger returns the mode's start trigger, or false if it has none.
func (d *Device) startTrigger(mode scheduleMode) (ScheduleTrigger, bool) {
	if mode == nightSchedule {
		if d.NightSchedule.Start != nil {
			return *d.NightSchedule.Start, true
		}
		start, _ := d.getNightScheduleBounds()
		return ScheduleTrigger{Kind: TriggerTime, Time: start}, true
	}
	if d.DimSchedule.Start != nil {
		return *d.DimSchedule.Start, true
	}
	if d.DimTime == nil || *d.DimTime == "" {
		return ScheduleTrigger{}, false
	}
	return ScheduleTrigger{Kind: TriggerTime, Time: *d.DimTime}, true
}

// endTriggerOn returns the mode's end trigger for an end on the given weekday.
func (d *Device) endTriggerOn(mode scheduleMode, day time.Weekday) ScheduleTrigger {
	schedule := &d.NightSchedule
	if mode == dimSchedule {
		schedule = &d.DimSchedule
	}
	if isWeekend(day) && schedule.WeekendEnd != nil {
		return *schedule.WeekendEnd
	}
	if schedule.End != nil {
		return *schedule.End
	}
	if mode == dimSchedule {
		return ScheduleTrigger{Kind: TriggerNightEnd}
	}
	_, end := d.getNightScheduleBounds()
	return ScheduleTrigger{Kind: TriggerTime, Time: end}
}

// concreteTrigger replaces a night_end reference with night mode's end
// trigger for the given day, or returns false if it can't be resolved.
func (d *Device) concreteTrigger(t ScheduleTrigger, day time.Weekday) (ScheduleTrigger, bool) {
	if t.Kind != TriggerNightEnd {
		return t, true
	}
	night := d.endTriggerOn(nightSchedule, day)
	return night, night.Kind != TriggerNightEnd // a night end can't follow itself
}

// sunFrameMinutes places a clock time in the 24 hours around a sun event,
// as minutes from the event day's midnight: 06:00-06:00 for sunset and
// 18:00 the evening before to 18:00 for sunrise.
func sunFrameMinutes(kind TriggerKind, minutes int) int {
	switch {
	case kind == TriggerSunset && minutes < 6*60:
		return minutes + 24*60
	case kind == TriggerSunrise && minutes >= 18*60:
		return minutes - 24*60
	}
	return minutes
}

func clampMinutes(t ScheduleTrigger, minutes int) int {
	if m, ok := clockToMinutes(t.NotBefore); ok {
		minutes = max(minutes, sunFrameMinutes(t.Kind, m))
	}
	if m, ok := clockToMinutes(t.NotAfter); ok {
		minutes = min(minutes, sunFrameMinutes(t.Kind, m))
	}
	return minutes
}

// nominalMinutes is where a trigger sits in the day regardless of season:
// sunrise counts as 06:00 and sunset as 18:00, plus offset and clamps.
func nominalMinutes(t ScheduleTrigger) int {
	switch t.Kind {
	case TriggerSunrise:
		return clampMinutes(t, 6*60+t.Offset)
	case TriggerSunset:
		return clampMinutes(t, 18*60+t.Offset)
	case TriggerTime:
		if m, ok := clockToMinutes(t.Time); ok {
			return m
		}
	}
	return 0
}

// resolveTrigger returns the wall-clock time at which the trigger fires for
// the given day (wall midnight), or false if it does not fire that day.
func (d *Device) resolveTrigger(t ScheduleTrigger, day time.Time, loc *time.Location) (time.Time, bool) {
	wall, _, ok := d.resolveTriggerRegime(t, day, loc)
	return wall, ok
}

// resolveTriggerRegime is resolveTrigger, also reporting the sun's regime
// for sun triggers.
func (d *Device) resolveTriggerRegime(t ScheduleTrigger, day time.Time, loc *time.Location) (time.Time, sunRegime, bool) {
	t, ok := d.concreteTrigger(t, day.Weekday())
	if !ok {
		return time.Time{}, sunRisesAndSets, false
	}
	switch t.Kind {
	case TriggerTime:
		m, ok := clockToMinutes(t.Time)
		return day.Add(time.Duration(m) * time.Minute), sunRisesAndSets, ok
	case TriggerSunrise, TriggerSunset:
		if !d.HasCoordinates() {
			return time.Time{}, sunRisesAndSets, false
		}
		event, regime := sunEventFor(t.Kind, d.Location.Lat, d.Location.Lng, day, loc)
		wall := wallOf(event.Add(time.Duration(t.Offset) * time.Minute).In(loc)).Truncate(time.Minute)
		minutes := int(wall.Sub(day) / time.Minute)
		return day.Add(time.Duration(clampMinutes(t, minutes)) * time.Minute), regime, true
	}
	return time.Time{}, sunRisesAndSets, false
}

// sunEventFor returns the sunrise or sunset of the solar day belonging to day
// (a wall-clock midnight), as an instant truncated to the minute. The event
// can land on the previous or next local date: at high latitudes in summer,
// an evening's sunset can come after midnight.
//
// In polar regions sunrise and sunset don't disappear, they converge: both at
// solar noon during polar night and both at solar midnight under the midnight
// sun, which is where they meet as those periods begin. Schedules therefore
// stay continuous, and offsets and clamps keep working: a sunset-to-sunrise
// night lasts all day in polar night and shrinks to nothing in midnight sun.
func sunEventFor(kind TriggerKind, lat, lng float64, day time.Time, loc *time.Location) (time.Time, sunRegime) {
	solarDay := day.Add(time.Duration(solarDayShift(lng, day.Year(), loc)) * wallDay)
	noon := sunrise.MeanSolarNoon(lng, solarDay.Year(), solarDay.Month(), solarDay.Day())
	anomaly := sunrise.SolarMeanAnomaly(noon)
	longitude := sunrise.EclipticLongitude(anomaly, sunrise.EquationOfCenter(anomaly), noon)
	transit := sunrise.SolarTransit(noon, anomaly, longitude)

	// Fraction of a day between solar noon and sunrise or sunset, as in
	// sunrise.SunriseSunset.
	var half float64
	regime := sunRisesAndSets
	switch hourAngle := sunrise.HourAngle(lat, sunrise.Declination(longitude)); hourAngle {
	case math.MaxFloat64:
		half, regime = 0, sunNeverRises
	case -math.MaxFloat64:
		half, regime = 0.5, sunNeverSets
	default:
		half = hourAngle / 360
	}
	event := transit + half
	if kind == TriggerSunrise {
		event = transit - half
	}
	return sunrise.JulianDayToTime(event).In(loc).Truncate(time.Minute), regime
}

// sunRegime says whether the sun rises and sets on a solar day.
type sunRegime int

const (
	sunRisesAndSets sunRegime = iota
	sunNeverRises             // polar night
	sunNeverSets              // midnight sun
)

// solarDayShift is how many days the solar day used for a local date differs
// from that date's UTC day: the one whose solar noon is nearest local noon.
// It uses the zone's standard offset so it never changes at a DST transition,
// which would otherwise reuse or skip a day where the zone is far from the
// location's solar time.
func solarDayShift(lng float64, year int, loc *time.Location) int {
	_, january := time.Date(year, time.January, 1, 12, 0, 0, 0, loc).Zone()
	_, july := time.Date(year, time.July, 1, 12, 0, 0, 0, loc).Zone()
	standard := time.Duration(min(january, july)) * time.Second
	solar := time.Duration(lng / 15 * float64(time.Hour))
	return -int(math.Round(float64(standard-solar) / float64(wallDay)))
}

// Limits for windows between a fixed time and a sun event ("mixed" windows),
// whose fixed side can end up on the wrong side of the sun as it moves
// through the year. Windows around the night (a sunset start or a sunrise
// end) are judged against solar noon; windows around the day (a sunrise start
// or a sunset end) by how far into darkness the fixed side reaches.
const (
	// lieInPastNoon is how far past solar noon a next-morning fixed end may
	// run after a sunset start: a weekend lie-in until 13:00 or later. A later
	// end belongs to the evening, and only pairs with that same evening,
	// unless it is where night mode ends (a dim "until night ends" before a
	// daytime night for a night-shift worker).
	lieInPastNoon = 3 * time.Hour
	// maxLeadIn and maxRunOn bound how far into darkness the fixed side of a
	// window around the day may reach before it starts or after it ends.
	maxLeadIn = 7 * time.Hour
	maxRunOn  = 9 * time.Hour
	// maxMixedWindow bounds every mixed window. A longer one that runs around
	// the night and across midnight has already passed the solar-noon checks,
	// so it is a long winter night (a late weekend lie-in in Helsinki, or
	// polar night): it is trimmed to just under maxMixedWindow, from the
	// sun's side, or from the end where night mode takes over before it.
	// Any other one means that day has no window.
	maxMixedWindow = 23 * time.Hour
	// convergedSlack is how far apart sunset and sunrise can be where they
	// have converged in polar night or under the midnight sun: rounding to
	// the minute and the drift between consecutive solar days.
	convergedSlack = 2 * time.Minute
)

// endsNextDay reports whether a window starting at start (on day) should try
// the next day's occurrence of end first.
//
// Two fixed times, or two sun events, use their nominal order: for fixed
// times that is the historical rule (an end before the start wraps
// overnight), and it keeps sunset-to-sunrise overnight everywhere. Between a
// fixed time and a sun event, the next day is tried first if today's end has
// already passed. After a sun start, it is also tried first if the nominal
// order says so: "sunset until 11:00" is a next-morning lie-in even where the
// sun sets at 10:55, at the edge of polar night. (A fixed start needs no such
// hint: "07:30 until sunrise" is that morning's sunrise unless it has
// passed.) windowFor falls back to today's end if that window doesn't work.
func (d *Device) endsNextDay(startTrigger ScheduleTrigger, start time.Time, end ScheduleTrigger, day time.Time, loc *time.Location) bool {
	nominal := nominalMinutes(end) < nominalMinutes(startTrigger)
	if startTrigger.IsSunBased() == end.IsSunBased() || (nominal && startTrigger.IsSunBased()) {
		return nominal
	}
	today, ok := d.resolveTrigger(end, day, loc)
	return ok && today.Before(start)
}

// solarNoon returns the midpoint of the day's plain sunrise and sunset, which
// is the solar transit in every regime (they converge on it in polar night
// and lie 12 hours either side of it under the midnight sun).
func (d *Device) solarNoon(day time.Time, loc *time.Location) time.Time {
	rise, set := d.sunTimes(day, loc)
	return rise.Add(set.Sub(rise) / 2)
}

// sunTimes returns the plain sunrise and sunset (no offset or clamp) for a
// day, as wall-clock times.
func (d *Device) sunTimes(day time.Time, loc *time.Location) (rise, set time.Time) {
	rise, _ = d.resolveTrigger(ScheduleTrigger{Kind: TriggerSunrise}, day, loc)
	set, _ = d.resolveTrigger(ScheduleTrigger{Kind: TriggerSunset}, day, loc)
	return rise, set
}

// darknessAtFixedSide returns how far into darkness the fixed side of a
// window around the day reaches: from a fixed start until the next sunrise,
// or from the last sunset until a fixed end. fixedAt is the fixed boundary on
// fixedDay; atStart says whether it is the window's start.
func (d *Device) darknessAtFixedSide(fixedAt, fixedDay time.Time, atStart bool, loc *time.Location) time.Duration {
	rise, set := d.sunTimes(fixedDay, loc)
	switch {
	case !fixedAt.Before(rise) && fixedAt.Before(set):
		return 0 // daylight
	case atStart && fixedAt.Before(rise):
		return rise.Sub(fixedAt)
	case atStart: // after sunset: until the next day's sunrise
		nextRise, _ := d.sunTimes(fixedDay.Add(wallDay), loc)
		return nextRise.Sub(fixedAt)
	case !fixedAt.Before(set):
		return fixedAt.Sub(set)
	default: // before sunrise: since the previous day's sunset
		_, prevSet := d.sunTimes(fixedDay.Add(-wallDay), loc)
		return fixedAt.Sub(prevSet)
	}
}

// scheduleWindow is one active period in wall-clock time. End is inclusive to
// the minute, to match the historical HH:MM comparison (an "00:00"-"23:59"
// schedule is active all day, and one ending at 06:00 is active until 06:01).
type scheduleWindow struct {
	Start, End time.Time
}

func (w scheduleWindow) contains(wall time.Time) bool {
	return !wall.Before(w.Start) && !wall.Truncate(time.Minute).After(w.End)
}

// windowFor builds the window that starts on day (a wall-clock midnight).
//
// Its end is either the next day's end trigger on the next day, if that
// trigger ends the next day (endsNextDay), or the same day's end trigger on
// the same day. The two triggers differ only around weekends: with a 23:00
// weekday end and an 08:00 weekend end, Friday runs to Saturday 08:00 and
// Sunday ends at 23:00. If neither fits, the next morning's trigger is used
// the same day. Windows between a fixed time and a sun event try those
// candidates in turn and take the first sensible one (mixedWindow); the next
// morning's trigger only stands in on the same day for an evening end, and
// not when the next day is a weekend day.
//
// There is no window that day if the start has passed the end, or if it is
// just sunset meeting sunrise where they have converged.
func (d *Device) windowFor(plan *schedulePlan, day time.Time, loc *time.Location) (scheduleWindow, bool) {
	start, startRegime, ok := d.resolveTriggerRegime(plan.start, day, loc)
	if !ok {
		return scheduleWindow{}, false
	}
	next := day.Add(wallDay)
	sameEnd, sameOK := d.concreteTrigger(plan.ends[weekendIndex(day.Weekday())], day.Weekday())
	nextEnd, nextOK := d.concreteTrigger(plan.ends[weekendIndex(next.Weekday())], next.Weekday())
	if !sameOK || !nextOK {
		return scheduleWindow{}, false
	}
	type candidate struct {
		day     time.Time
		trigger ScheduleTrigger
	}
	followsNight := func(c candidate) bool { return plan.ends[weekendIndex(c.day.Weekday())].Kind == TriggerNightEnd }
	var candidates []candidate
	nextFirst := d.endsNextDay(plan.start, start, nextEnd, day, loc)
	if plan.start.IsSunBased() == sameEnd.IsSunBased() && plan.start.IsSunBased() == nextEnd.IsSunBased() {
		switch {
		case nextFirst:
			candidates = []candidate{{next, nextEnd}}
		case !d.endsNextDay(plan.start, start, sameEnd, day, loc):
			candidates = []candidate{{day, sameEnd}}
		default:
			candidates = []candidate{{day, nextEnd}}
		}
	} else {
		if nextFirst {
			candidates = append(candidates, candidate{next, nextEnd})
		}
		candidates = append(candidates, candidate{day, sameEnd})
		// The next day's trigger on this day only stands in for a same-day
		// end (a Sunday evening's weekday end), never for a next-morning one.
		if nominalMinutes(nextEnd) >= nominalMinutes(plan.start) && !isWeekend(next.Weekday()) {
			candidates = append(candidates, candidate{day, nextEnd})
		}
	}

	// A weekend end on the morning after a sun start is a lie-in when the
	// weekday end is a next-morning one: it may run past solar noon until the
	// next day's start ("dim from sunset until 08:00, 17:00 at weekends").
	lieIn := func(c candidate) bool {
		weekday, ok := d.concreteTrigger(plan.ends[0], c.day.Weekday())
		return ok && c.day.After(day) && isWeekend(c.day.Weekday()) && plan.ends[0] != plan.ends[1] &&
			nominalMinutes(weekday) < nominalMinutes(plan.start)
	}
	for _, c := range candidates {
		end, endRegime, ok := d.resolveTriggerRegime(c.trigger, c.day, loc)
		switch {
		case !ok || end.Before(start):
			continue
		case startRegime == endRegime && startRegime != sunRisesAndSets && end.Sub(start) <= convergedSlack:
			continue
		case plan.start.IsSunBased() == c.trigger.IsSunBased():
			return scheduleWindow{Start: start, End: end}, true
		}
		var lieInUntil time.Time
		if lieIn(c) {
			lieInUntil, _ = d.resolveTrigger(plan.start, c.day, loc)
		}
		if w, ok := d.mixedWindow(plan.start, c.trigger, start, end, day, c.day, loc, followsNight(c), lieInUntil); ok {
			return w, true
		}
	}
	return scheduleWindow{}, false
}

// mixedWindow checks a window between a fixed time and a sun event (see the
// limits above), and trims or rejects one of maxMixedWindow or more.
func (d *Device) mixedWindow(startTrigger, endTrigger ScheduleTrigger, start, end, day, endDay time.Time, loc *time.Location, followsNight bool, lieInUntil time.Time) (scheduleWindow, bool) {
	sunAtStart := startTrigger.IsSunBased()
	aroundNight := (sunAtStart && startTrigger.Kind == TriggerSunset) || (!sunAtStart && endTrigger.Kind == TriggerSunrise)
	overnight := endDay.After(day)
	switch {
	case aroundNight && sunAtStart:
		// After a sunset, a fixed end is either the next morning's (until
		// lieInPastNoon past solar noon) or the same evening's (from solar
		// noon on): "sunset until 07:00", "sunset until 22:00".
		// A later next-day end is only kept where it is night mode's end
		// and that night starts after this window (a daytime night).
		if overnight && end.After(d.solarNoon(endDay, loc).Add(lieInPastNoon)) &&
			(!followsNight || !d.nightWindowEndsAfter(start, end, endDay, loc)) &&
			(lieInUntil.IsZero() || !end.Before(lieInUntil)) {
			return scheduleWindow{}, false
		}
		if !overnight && end.Before(d.solarNoon(day, loc)) {
			return scheduleWindow{}, false
		}
	case aroundNight:
		// Before a sunrise, a fixed start from solar noon on waits for the
		// next morning ("22:00 until sunrise"); an earlier one is that same
		// morning's ("06:30 until sunrise", only while it is still dark).
		if overnight == start.Before(d.solarNoon(day, loc)) {
			return scheduleWindow{}, false
		}
	case sunAtStart:
		// From a sunrise to a fixed end. A same-day end before sunrise is a
		// pre-dawn window ("sunrise-60 until 07:00"), and an overnight end
		// nominally before the sunrise is the next early morning ("off from
		// sunrise until opening at 05:00"); neither has darkness to bound.
		rise, _ := d.sunTimes(endDay, loc)
		preDawn := !overnight && end.Before(rise)
		nextMorning := overnight && nominalMinutes(endTrigger) < nominalMinutes(startTrigger)
		if !preDawn && !nextMorning && d.darknessAtFixedSide(end, endDay, false, loc) >= maxRunOn {
			return scheduleWindow{}, false
		}
	default:
		// From a fixed start to a sunset. A same-day window starts before the
		// sunset by construction, and an overnight one whose start is
		// nominally after the sunset is an evening start ("off from 23:00
		// until sunset"); only an overnight one whose start is nominally
		// before the sunset has had the sun move past it.
		if overnight && nominalMinutes(endTrigger) >= nominalMinutes(startTrigger) &&
			d.darknessAtFixedSide(start, day, true, loc) >= maxLeadIn {
			return scheduleWindow{}, false
		}
	}
	if end.Sub(start) < maxMixedWindow {
		return scheduleWindow{Start: start, End: end}, true
	}

	if !aroundNight || !overnight {
		return scheduleWindow{}, false
	}
	if sunAtStart {
		// Trim the start, except before night mode: if night mode takes
		// over before the trimmed end, the tail is hidden behind it, so cut
		// that and keep the evening ("dim from sunset until night mode
		// starts at 08:00" for a night-shift worker).
		if trimmed := start.Add(maxMixedWindow - time.Minute); followsNight && d.nightStartsBy(end, endDay, trimmed.Add(time.Minute), loc) {
			end = trimmed
		} else {
			start = end.Add(-maxMixedWindow + time.Minute)
		}
	} else {
		end = start.Add(maxMixedWindow - time.Minute)
	}
	return scheduleWindow{Start: start, End: end}, true
}

func wallMidnight(wall time.Time) time.Time {
	return time.Date(wall.Year(), wall.Month(), wall.Day(), 0, 0, 0, 0, time.UTC)
}

// scheduleActiveAt reports whether the mode's schedule covers at, in at's
// location.
func (d *Device) scheduleActiveAt(mode scheduleMode, at time.Time) bool {
	plan, ok := d.planFor(mode)
	if !ok {
		return false
	}
	return d.planActiveAt(&plan, at)
}

// planActiveAt checks the windows belonging to the two days before through the
// next day: a window can start the evening before its date (sunrise minus an
// offset) and end as late as two days after it (an overnight end whose sun
// event lands on the following date, for a device whose zone is far from its
// location's solar time).
func (d *Device) planActiveAt(plan *schedulePlan, at time.Time) bool {
	wall := wallOf(at)
	today := wallMidnight(wall)
	for offset := -2; offset <= 1; offset++ {
		if w, ok := d.windowFor(plan, today.Add(time.Duration(offset)*wallDay), at.Location()); ok && w.contains(wall) {
			return true
		}
	}
	return false
}

// changesAt reports whether the schedule's state changes at c: just before
// it, or within the inclusive end minute that starts at c.
func (d *Device) changesAt(plan *schedulePlan, c time.Time) bool {
	now := d.planActiveAt(plan, c)
	return now != d.planActiveAt(plan, c.Add(-time.Nanosecond)) || now != d.planActiveAt(plan, c.Add(time.Minute))
}

// NextChangeAt lookahead: start with a week and double up to 512 days, for
// schedules that stay on or off for weeks or months (sunset to sunrise in
// polar night, "06:30 to sunrise" through the summer).
const (
	nextChangeLookaheadDays    = 8
	maxNextChangeLookaheadDays = 512
)

// scheduleNextChangeAt returns the first start or end after "at" where the
// mode actually turns on or off; manual overrides use it as their expiry.
// Windows that overlap or touch are not changes, nor are boundaries that fall
// inside a skipped DST hour. If nothing changes within the lookahead (an
// all-day "00:00"-"23:59" schedule), it returns the first boundary after at,
// as before. It returns nil if there is no window at all within the
// lookahead (a sun trigger without coordinates, or "06:30 to sunrise" in
// Quito), which manual overrides report as an incomplete schedule.
func (d *Device) scheduleNextChangeAt(mode scheduleMode, at time.Time) *time.Time {
	plan, ok := d.planFor(mode)
	if !ok {
		return nil
	}
	loc := at.Location()
	today := wallMidnight(wallOf(at))
	var first *time.Time
	for lookahead := nextChangeLookaheadDays; lookahead <= maxNextChangeLookaheadDays; lookahead *= 2 {
		windows := make([]scheduleWindow, 0, lookahead+3)
		for offset := -2; offset <= lookahead; offset++ {
			if w, ok := d.windowFor(&plan, today.Add(time.Duration(offset)*wallDay), loc); ok {
				windows = append(windows, w)
			}
		}
		if first == nil {
			first = firstBoundaryAfter(windows, at, loc)
		}
		if first == nil && !plan.usesSun {
			return nil // fixed windows repeat weekly: none this week means none ever
		}
		merged := mergeOverlapping(windows)
		for i, w := range merged {
			// Windows a minute apart touch (the end minute is inclusive), so
			// only boundaries next to a gap can be changes. The last window's
			// end is unknown until the lookahead grows.
			startEdge := i == 0 || w.Start.Sub(merged[i-1].End) > time.Minute
			endEdge := i+1 < len(merged) && merged[i+1].Start.Sub(w.End) > time.Minute
			candidates := [3]struct {
				at   time.Time
				edge bool
			}{
				{instantAtWall(w.Start, loc), startEdge},
				{instantAtWall(w.End, loc), endEdge},
				// An end in the last minute before the clock falls back
				// really ends at that minute's second occurrence.
				{instantAtWall(w.End.Add(time.Minute), loc).Add(-time.Minute), endEdge},
			}
			for _, candidate := range candidates {
				if !candidate.at.After(at) {
					continue
				}
				// Confirm in real time too: a window inside a skipped DST
				// hour never happens.
				if candidate.edge && d.changesAt(&plan, candidate.at) {
					return &candidate.at
				}
			}
		}
	}
	return first
}

// firstBoundaryAfter returns the earliest window start or end after at, or nil.
func firstBoundaryAfter(windows []scheduleWindow, at time.Time, loc *time.Location) *time.Time {
	var first *time.Time
	for _, w := range windows {
		for _, boundary := range [2]time.Time{w.Start, w.End} {
			if c := instantAtWall(boundary, loc); c.After(at) && (first == nil || c.Before(*first)) {
				first = &c
			}
		}
	}
	return first
}

// mergeOverlapping sorts windows by start and merges those that overlap.
func mergeOverlapping(windows []scheduleWindow) []scheduleWindow {
	slices.SortFunc(windows, func(a, b scheduleWindow) int { return a.Start.Compare(b.Start) })
	merged := windows[:0]
	for _, w := range windows {
		if n := len(merged); n > 0 && !w.Start.After(merged[n-1].End) {
			if w.End.After(merged[n-1].End) {
				merged[n-1].End = w.End
			}
			continue
		}
		merged = append(merged, w)
	}
	return merged
}

// nightWindowEndingAt returns night mode's window that ends at end, if any.
func (d *Device) nightWindowEndingAt(end, endDay time.Time, loc *time.Location) (scheduleWindow, bool) {
	nightPlan, ok := d.planFor(nightSchedule)
	if !ok || nightPlan.ends[0].Kind == TriggerNightEnd || nightPlan.ends[1].Kind == TriggerNightEnd {
		return scheduleWindow{}, false
	}
	for _, day := range [2]time.Time{endDay.Add(-wallDay), endDay} {
		if w, ok := d.windowFor(&nightPlan, day, loc); ok && w.End.Equal(end) {
			return w, true
		}
	}
	return scheduleWindow{}, false
}

// nightWindowEndsAfter reports whether night mode has a window ending at end
// that starts at or after start: a dim "until night ends" leads into it.
func (d *Device) nightWindowEndsAfter(start, end, endDay time.Time, loc *time.Location) bool {
	w, ok := d.nightWindowEndingAt(end, endDay, loc)
	return ok && !w.Start.Before(start)
}

// nightStartsBy reports whether night mode's window ending at end starts no
// later than by.
func (d *Device) nightStartsBy(end, endDay, by time.Time, loc *time.Location) bool {
	w, ok := d.nightWindowEndingAt(end, endDay, loc)
	return ok && !w.Start.After(by)
}
