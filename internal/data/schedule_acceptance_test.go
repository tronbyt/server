package data

import (
	"testing"
	"time"
)

// Sensible schedules and what they should do at a given time and place, read
// literally. Each is a configuration a person would plausibly choose, and
// together they cover the ways a fixed time and a sun event can pair up as
// the sun moves through the year.
func TestSensibleSchedulesAtTheirLocations(t *testing.T) {
	stockholm := place{59.3293, 18.0686, "Europe/Stockholm"}
	madrid := place{40.4168, -3.7038, "Europe/Madrid"}
	moscow := place{55.7558, 37.6173, "Europe/Moscow"}
	chicago := place{41.8781, -87.6298, "America/Chicago"}
	singapore := place{1.3521, 103.8198, "Asia/Singapore"}
	quito := place{-0.1807, -78.4678, "America/Guayaquil"}
	kirkenes := place{69.7271, 30.0450, "Europe/Oslo"}

	sun := func(kind TriggerKind, offset int) *ScheduleTrigger {
		return &ScheduleTrigger{Kind: kind, Offset: offset}
	}
	night := func(start, end, weekend *ScheduleTrigger) func(*Device) {
		return func(d *Device) {
			d.NightModeEnabled = true
			d.NightSchedule = ModeSchedule{Start: start, End: end, WeekendEnd: weekend}
		}
	}
	dim := func(start, end, weekend *ScheduleTrigger) func(*Device) {
		return func(d *Device) {
			d.DimModeEnabled = true
			d.DimSchedule = ModeSchedule{Start: start, End: end, WeekendEnd: weekend}
		}
	}
	// Dim from start until night mode ends; night 23:00-07:00 with a lie-in.
	dimUntilNight := func(start *ScheduleTrigger, lieIn string) func(*Device) {
		return func(d *Device) {
			d.NightModeEnabled = true
			d.NightStart, d.NightEnd = "23:00", "07:00"
			d.NightSchedule.WeekendEnd = fixed(lieIn)
			d.DimModeEnabled = true
			d.DimSchedule.Start = start
		}
	}
	// A night-shift worker: night mode by day, dim from start until it begins.
	nightShift := func(nightStart, nightEnd string, start *ScheduleTrigger) func(*Device) {
		return func(d *Device) {
			d.NightModeEnabled = true
			d.NightStart, d.NightEnd = nightStart, nightEnd
			d.DimModeEnabled = true
			d.DimSchedule.Start = start
		}
	}

	cases := []struct {
		name   string
		p      place
		set    func(*Device)
		at     string // local time
		active bool
		dim    bool // check dim mode instead of the night schedule
	}{
		// Weekend lie-ins, including long winter nights.
		{"Stockholm sunset to 07:00, 13:00 lie-in, Friday night in June", stockholm, night(trig(TriggerSunset), fixed("07:00"), fixed("13:00")), "2026-06-19 23:30", true, false},
		{"Helsinki dim 2h before sunset until night ends, 13:00 lie-in, December Friday", helsinki, dimUntilNight(sun(TriggerSunset, -120), "13:00"), "2026-12-18 13:30", true, true},
		{"Kirkenes dim from sunset until night ends, 11:00 lie-in, polar-night Friday", kirkenes, dimUntilNight(trig(TriggerSunset), "11:00"), "2026-12-18 22:30", true, true},
		{"Tromso dim an hour before sunset until night ends, 11:00 lie-in, Friday", tromso, dimUntilNight(sun(TriggerSunset, -60), "11:00"), "2026-01-16 20:00", true, true},

		// Evening windows that end before a late summer sunset.
		{"Madrid dim 2h before sunset until 21:00", madrid, dim(sun(TriggerSunset, -120), fixed("21:00"), nil), "2026-06-21 20:30", true, true},
		{"Reykjavik dim 2h before sunset until 23:00", reykjavik, dim(sun(TriggerSunset, -120), fixed("23:00"), nil), "2026-06-21 22:30", true, true},

		// Mornings: dim only while it is dark.
		{"Boston dim 06:30 until sunrise, winter morning", boston, dim(fixed("06:30"), trig(TriggerSunrise), nil), "2026-12-21 06:45", true, true},
		{"Boston dim 07:00 until an hour after sunrise, summer noon", boston, dim(fixed("07:00"), sun(TriggerSunrise, 60), nil), "2026-06-21 12:00", false, true},
		{"Moscow dim 09:00 until sunrise, summer afternoon", moscow, dim(fixed("09:00"), trig(TriggerSunrise), nil), "2026-06-21 15:00", false, true},
		{"Quito dim 06:30 until 30 min after sunrise", quito, dim(fixed("06:30"), sun(TriggerSunrise, 30), nil), "2026-03-01 06:35", true, true},
		{"Moscow dim 06:30 until sunrise, 08:00 on weekends, summer Friday", moscow, dim(fixed("06:30"), trig(TriggerSunrise), fixed("08:00")), "2026-06-19 07:15", false, true},
		{"Moscow dim 06:30 until sunrise, 08:00 on weekends, summer Saturday", moscow, dim(fixed("06:30"), trig(TriggerSunrise), fixed("08:00")), "2026-06-20 07:15", true, true},

		// A sunset offset that passes a bedtime end must not dim all day.
		{"Helsinki dim 2h after sunset until 23:00, summer afternoon", helsinki, dim(sun(TriggerSunset, 120), fixed("23:00"), nil), "2026-06-21 13:00", false, true},
		{"Helsinki dim 2h after sunset until 23:00, winter evening", helsinki, dim(sun(TriggerSunset, 120), fixed("23:00"), nil), "2026-12-21 20:00", true, true},

		// Night-shift workers and displays outside the bedroom.
		{"Chicago night 08:00-16:00, dim an hour after sunset, winter evening", chicago, nightShift("08:00", "16:00", sun(TriggerSunset, 60)), "2026-12-21 18:00", true, true},
		{"Boston night 08:00-16:00, dim at sunset, just after dark", boston, nightShift("08:00", "16:00", trig(TriggerSunset)), "2026-12-21 16:30", true, true},
		{"Singapore sign off from 23:00 until sunset, midday", singapore, night(fixed("23:00"), trig(TriggerSunset), nil), "2026-03-10 12:00", true, false},
		{"Singapore sign off from 23:00 until sunset, lit in the evening", singapore, night(fixed("23:00"), trig(TriggerSunset), nil), "2026-03-10 20:00", false, false},
		{"Singapore dim from sunset until 08:00, 17:00 at weekends, Saturday noon", singapore, dim(trig(TriggerSunset), fixed("08:00"), fixed("17:00")), "2026-03-14 12:00", true, true},
		{"Boston night 22:00 until sunrise", boston, night(fixed("22:00"), trig(TriggerSunrise), nil), "2026-06-21 04:00", true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			device := deviceAt(tc.p)
			tc.set(&device)
			at, err := time.ParseInLocation("2006-01-02 15:04", tc.at, mustLoad(t, tc.p.tz))
			if err != nil {
				t.Fatal(err)
			}
			got := device.GetScheduledNightModeIsActiveAt(at)
			if tc.dim {
				got = device.GetDimModeIsActiveAt(at)
			}
			if got != tc.active {
				t.Errorf("at %s: active = %v, want %v", tc.at, got, tc.active)
			}
		})
	}
}
