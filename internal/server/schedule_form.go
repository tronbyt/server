package server

import (
	"fmt"
	"net/http"
	"strconv"

	"tronbyt-server/internal/data"
)

// scheduleTriggerFromForm reads one night or dim mode boundary from the
// device settings form: <prefix>_kind selects a fixed time (read from
// timeField), sunrise or sunset (with <prefix>_offset in minutes and the
// optional <prefix>_not_before and <prefix>_not_after clamps), or night_end.
// It returns nil when the kind or the fixed time is empty, which means "not
// set" (the legacy field or the default applies), so forms that predate it
// keep working and a time can still be cleared.
func scheduleTriggerFromForm(r *http.Request, prefix, timeField string) (*data.ScheduleTrigger, error) {
	kind := data.TriggerKind(r.FormValue(prefix + "_kind"))
	switch kind {
	case "":
		return nil, nil
	case data.TriggerNightEnd:
		return &data.ScheduleTrigger{Kind: kind}, nil
	case data.TriggerTime:
		raw := r.FormValue(timeField)
		if raw == "" {
			return nil, nil // a cleared time: the legacy field or default decides
		}
		value, err := parseTimeInput(raw)
		if err != nil {
			return nil, err
		}
		return &data.ScheduleTrigger{Kind: kind, Time: value}, nil
	case data.TriggerSunrise, data.TriggerSunset:
		trigger := &data.ScheduleTrigger{Kind: kind}
		if value := r.FormValue(prefix + "_offset"); value != "" {
			offset, err := strconv.Atoi(value)
			if err != nil {
				return nil, fmt.Errorf("invalid offset %q", value)
			}
			trigger.Offset = offset
		}
		for _, clamp := range []struct {
			field string
			dest  *string
		}{{"_not_before", &trigger.NotBefore}, {"_not_after", &trigger.NotAfter}} {
			if value := r.FormValue(prefix + clamp.field); value != "" {
				parsed, err := parseTimeInput(value)
				if err != nil {
					return nil, err
				}
				*clamp.dest = parsed
			}
		}
		return trigger, nil
	}
	return nil, fmt.Errorf("unknown schedule kind %q", kind)
}

// sunTriggerOnly keeps a trigger only if it is not a fixed time, for
// boundaries whose fixed time is stored in a legacy HH:MM field.
func sunTriggerOnly(trigger *data.ScheduleTrigger) *data.ScheduleTrigger {
	if trigger != nil && trigger.Kind == data.TriggerTime {
		return nil
	}
	return trigger
}

// modeScheduleFromForm reads a night or dim schedule's start, end and weekend
// end from the device settings form. Fixed start and end times stay in the
// legacy fields, which the caller parses.
func modeScheduleFromForm(r *http.Request, prefix, startTimeField, endTimeField string, endHasLegacyField bool) (data.ModeSchedule, error) {
	var schedule data.ModeSchedule
	start, err := scheduleTriggerFromForm(r, prefix+"_start", startTimeField)
	if err != nil {
		return schedule, fmt.Errorf("start: %w", err)
	}
	end, err := scheduleTriggerFromForm(r, prefix+"_end", endTimeField)
	if err != nil {
		return schedule, fmt.Errorf("end: %w", err)
	}
	weekendEnd, err := scheduleTriggerFromForm(r, prefix+"_weekend_end", prefix+"_weekend_end_time")
	if err != nil {
		return schedule, fmt.Errorf("weekend end: %w", err)
	}
	schedule.Start = sunTriggerOnly(start)
	schedule.End = end
	if endHasLegacyField {
		schedule.End = sunTriggerOnly(end)
	}
	schedule.WeekendEnd = weekendEnd
	return schedule, nil
}
