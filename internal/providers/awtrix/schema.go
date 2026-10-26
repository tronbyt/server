package awtrix

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// AwtrixConfigResponse represents the JSON returned by GET /api/v1/apps/.../config.
type AwtrixConfigResponse struct {
	Name     string        `json:"name"`
	Fields   []AwtrixField `json:"fields"`
	Warnings []string      `json:"warnings"`
}

// AwtrixField represents a single configuration field in an Awtrix app.
type AwtrixField struct {
	Key      string   `json:"key"`
	Type     string   `json:"type"`
	Label    string   `json:"label,omitempty"`
	Help     string   `json:"help,omitempty"`
	Options  []any    `json:"options,omitempty"`
	Group    string   `json:"group,omitempty"`
	Unit     string   `json:"unit,omitempty"`
	Path     []string `json:"path,omitempty"`
	Default  any      `json:"default,omitempty"`
	Value    any      `json:"value,omitempty"`
	Nullable bool     `json:"nullable,omitempty"`
	Min      *float64 `json:"min,omitempty"`
	Max      *float64 `json:"max,omitempty"`
	Step     *float64 `json:"step,omitempty"`
}

type appFieldMeta struct {
	label       string
	description string
	options     [][2]string
}

// builtinFieldMeta mirrors the AWTRIX NG UI metadata for standard built-in clock & calendar fields.
var builtinFieldMeta = map[string]appFieldMeta{
	"timeMode": {
		label:       "Clock layout",
		description: "What the clock app draws.",
		options: [][2]string{
			{"0", "Time only"},
			{"1", "Calendar, weekday bar below"},
			{"2", "Calendar, weekday bar on top"},
			{"3", "Spiral calendar, weekday below"},
			{"4", "Spiral calendar, weekday on top"},
			{"5", "Big clock"},
			{"6", "Binary clock"},
		},
	},
	"clockFace": {
		label:       "Clock face",
		description: "Visual clock face style.",
		options: [][2]string{
			{"sheet", "Calendar sheet"},
			{"ring", "Ring calendar"},
			{"flap", "Split-flap"},
			{"month", "Month sheet"},
			{"big", "Big clock"},
		},
	},
	"time24h": {
		label:       "24-hour clock",
		description: "Use 24-hour time format (off = 12-hour AM/PM).",
	},
	"timeLeadingZero": {
		label:       "Leading zero",
		description: "Show 07:05 instead of 7:05.",
	},
	"timeShowSeconds": {
		label:       "Show seconds",
		description: "Display second digits in layouts that support them.",
	},
	"timeShowAmPm": {
		label:       "AM/PM indicator",
		description: "Show AM/PM in 12-hour clock mode.",
	},
	"timeSeparatorMode": {
		label:       "Time separator mode",
		description: "Behavior of the colon between hours and minutes.",
		options: [][2]string{
			{"steady", "Steady on"},
			{"blink", "Blinking every second"},
			{"pulse", "Pulsing (soft fade)"},
		},
	},
	"dateOrder": {
		label:       "Date order",
		description: "Order of day, month, and year.",
		options: [][2]string{
			{"dayMonthYear", "Day, month, year (31.12.25)"},
			{"monthDayYear", "Month, day, year (12/31/25)"},
			{"yearMonthDay", "Year, month, day (25-12-31)"},
		},
	},
	"dateSeparator": {
		label:       "Date separator",
		description: "Character between day, month, and year.",
		options: [][2]string{
			{"dot", "Dot (31.12.)"},
			{"slash", "Slash (31/12)"},
			{"dash", "Dash (31-12)"},
		},
	},
	"dateYearMode": {
		label:       "Year in date",
		description: "How many digits to display for the year.",
		options: [][2]string{
			{"none", "Hidden"},
			{"twoDigit", "2 digits (25)"},
			{"fourDigit", "4 digits (2025)"},
		},
	},
	"dateShowWeekday": {
		label:       "Weekday prefix",
		description: "Short weekday name before the date (e.g. Wed 31.12.).",
	},
	"dateMonthNames": {
		label:       "Month as name",
		description: "Show '31 Dec' instead of numbers '31.12'.",
	},
	"weekdayBar.show": {
		label:       "Weekday bar",
		description: "Show seven marks at the bottom, one per weekday.",
	},
	"weekdayBar.startOnMonday": {
		label:       "Week starts Monday",
		description: "Start the weekday bar on Monday instead of Sunday.",
	},
	"timeColor": {
		label:       "Time color",
		description: "Custom color for time digits (default: white).",
	},
	"dateColor": {
		label:       "Date color",
		description: "Custom color for date digits (default: white).",
	},
	"calendarHeaderColor": {
		label:       "Calendar header color",
		description: "Color for top of the calendar sheet.",
	},
	"calendarTextColor": {
		label:       "Calendar text color",
		description: "Color for day number in the calendar.",
	},
	"calendarBodyColor": {
		label:       "Calendar body color",
		description: "Background color of the calendar sheet.",
	},
	"weekdayBar.activeColor": {
		label:       "Today's weekday color",
		description: "Highlight color for the current weekday.",
	},
	"weekdayBar.inactiveColor": {
		label:       "Other weekdays color",
		description: "Color for other days of the week.",
	},
	"useCelsius": {
		label:       "Use Celsius",
		description: "Show temperature in Celsius (off = Fahrenheit).",
	},
}

// PixletSchema represents the schema format used by Tronbyt.
type PixletSchema struct {
	Version string              `json:"version"`
	Schema  []PixletSchemaField `json:"schema"`
}

type PixletSchemaField struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Icon        string         `json:"icon,omitempty"`
	Type        string         `json:"type"`
	Default     any            `json:"default,omitempty"`
	Options     []PixletOption `json:"options,omitempty"`
}

type PixletOption struct {
	Display string `json:"display"`
	Value   string `json:"value"`
}

// TranslateAwtrixSchema converts an AWTRIX NG config response into a Pixlet-compatible schema JSON.
func TranslateAwtrixSchema(resp AwtrixConfigResponse, isBuiltin bool) ([]byte, error) {
	var schemaFields []PixletSchemaField

	for _, f := range resp.Fields {
		// Use dot-separated path or key as unique ID
		fieldID := f.Key
		if len(f.Path) > 0 {
			fieldID = strings.Join(f.Path, ".")
		}

		name := f.Label
		desc := f.Help
		var optionPairs [][2]string

		if isBuiltin {
			if meta, ok := builtinFieldMeta[fieldID]; ok {
				if name == "" {
					name = meta.label
				}
				if desc == "" {
					desc = meta.description
				}
				optionPairs = meta.options
			}
		}

		if name == "" {
			name = titleCase(fieldID)
		}

		field := PixletSchemaField{
			ID:          fieldID,
			Name:        name,
			Description: desc,
			Icon:        iconForField(fieldID, f.Type),
		}

		switch f.Type {
		case "bool":
			field.Type = "onoff"
			if d, ok := f.Default.(bool); ok {
				field.Default = fmt.Sprintf("%t", d)
			} else if v, ok := f.Value.(bool); ok {
				field.Default = fmt.Sprintf("%t", v)
			} else {
				field.Default = "false"
			}

		case "select":
			field.Type = "dropdown"
			var opts []PixletOption
			for _, o := range f.Options {
				valStr := fmt.Sprintf("%v", o)
				dispStr := valStr

				// Look up human-readable display string
				for _, p := range optionPairs {
					if p[0] == valStr {
						dispStr = p[1]
						break
					}
				}
				opts = append(opts, PixletOption{
					Display: dispStr,
					Value:   valStr,
				})
			}
			field.Options = opts
			if f.Default != nil {
				field.Default = fmt.Sprintf("%v", f.Default)
			} else if f.Value != nil {
				field.Default = fmt.Sprintf("%v", f.Value)
			}

		case "color":
			field.Type = "color"
			if hexStr := colorToHex(f.Default); hexStr != "" {
				field.Default = hexStr
			} else if hexStr := colorToHex(f.Value); hexStr != "" {
				field.Default = hexStr
			} else {
				field.Default = "#ffffff"
			}

		case "number", "slider":
			field.Type = "text"
			if f.Unit != "" && desc != "" {
				field.Description = fmt.Sprintf("%s (Unit: %s)", desc, f.Unit)
			} else if f.Unit != "" {
				field.Description = fmt.Sprintf("Unit: %s", f.Unit)
			}
			if f.Default != nil {
				field.Default = fmt.Sprintf("%v", f.Default)
			} else if f.Value != nil {
				field.Default = fmt.Sprintf("%v", f.Value)
			}

		default:
			field.Type = "text"
			if f.Default != nil {
				field.Default = fmt.Sprintf("%v", f.Default)
			}
		}

		schemaFields = append(schemaFields, field)
	}

	// Add per-app display layout option
	schemaFields = append(schemaFields, PixletSchemaField{
		ID:          "_layout",
		Name:        "Display Layout",
		Description: "Positioning & scaling mode (default uses server setting)",
		Type:        "dropdown",
		Default:     "default",
		Options: []PixletOption{
			{Display: "Server Default", Value: "default"},
			{Display: "Auto-Center & Pad", Value: "center"},
			{Display: "Stretch & Fill", Value: "fill"},
			{Display: "Native Top-Left", Value: "native"},
		},
	})

	pixSchema := PixletSchema{
		Version: "1",
		Schema:  schemaFields,
	}

	return json.Marshal(pixSchema)
}

// ConvertUserConfigToAwtrix transforms Tronbyt config values into the structure expected by Awtrix PATCH.
func ConvertUserConfigToAwtrix(fields []AwtrixField, userConfig map[string]any) map[string]any {
	body := make(map[string]any)

	fieldMap := make(map[string]AwtrixField)
	for _, f := range fields {
		k := f.Key
		if len(f.Path) > 0 {
			k = strings.Join(f.Path, ".")
		}
		fieldMap[k] = f
	}

	for k, v := range userConfig {
		if strings.HasPrefix(k, "_") {
			continue
		}
		f, ok := fieldMap[k]
		if !ok {
			continue // Whitelist only: discard unmapped keys to prevent blind parameter injection
		}
		path := []string{k}
		if len(f.Path) > 0 {
			path = f.Path
		}

		// Convert value based on target field type
		converted := v
		switch f.Type {
		case "bool":
			if b, ok := v.(bool); ok {
				converted = b
			} else if s, ok := v.(string); ok {
				converted = strings.EqualFold(s, "true") || s == "1"
			}
		case "color":
			if s, ok := v.(string); ok {
				converted = hexToColorInt(s)
			}
		case "select":
			// If options are numbers, convert string back to number
			if len(f.Options) > 0 {
				if _, isNum := f.Options[0].(float64); isNum {
					if s, ok := v.(string); ok {
						if n, err := strconv.Atoi(s); err == nil {
							converted = n
						}
					}
				}
			}
		case "number", "slider":
			if s, ok := v.(string); ok {
				if strings.Contains(s, ".") {
					if fl, err := strconv.ParseFloat(s, 64); err == nil {
						converted = fl
					}
				} else {
					if n, err := strconv.Atoi(s); err == nil {
						converted = n
					} else if fl, err := strconv.ParseFloat(s, 64); err == nil {
						converted = fl
					}
				}
			}
		}

		// Build nested object hierarchy along path
		target := body
		for i := 0; i < len(path)-1; i++ {
			step := path[i]
			if next, ok := target[step].(map[string]any); ok {
				target = next
			} else {
				next := make(map[string]any)
				target[step] = next
				target = next
			}
		}
		target[path[len(path)-1]] = converted
	}

	return body
}

func colorToHex(val any) string {
	if val == nil {
		return ""
	}
	switch v := val.(type) {
	case string:
		if strings.HasPrefix(v, "#") {
			return v
		}
		if len(v) == 6 {
			return "#" + v
		}
	case float64:
		return fmt.Sprintf("#%06x", int(v)&0xFFFFFF)
	case int:
		return fmt.Sprintf("#%06x", v&0xFFFFFF)
	}
	return ""
}

func hexToColorInt(hexStr string) int {
	clean := strings.TrimPrefix(hexStr, "#")
	if len(clean) == 3 {
		// Expand shorthand #RGB -> #RRGGBB
		clean = string([]byte{clean[0], clean[0], clean[1], clean[1], clean[2], clean[2]})
	}
	if len(clean) != 6 {
		return 0xFFFFFF
	}
	b, err := hex.DecodeString(clean)
	if err != nil || len(b) != 3 {
		return 0xFFFFFF
	}
	return (int(b[0]) << 16) | (int(b[1]) << 8) | int(b[2])
}

func titleCase(s string) string {
	parts := strings.Split(s, ".")
	last := parts[len(parts)-1]
	// Insert space before uppercase letters
	var b strings.Builder
	for i, r := range last {
		if i > 0 && r >= 'A' && r <= 'Z' {
			b.WriteByte(' ')
		}
		b.WriteRune(r)
	}
	res := b.String()
	if len(res) > 0 {
		return strings.ToUpper(res[:1]) + res[1:]
	}
	return s
}

func iconForField(fieldID, fieldType string) string {
	lower := strings.ToLower(fieldID)
	switch {
	case strings.Contains(lower, "time") || strings.Contains(lower, "clock"):
		return "clock"
	case strings.Contains(lower, "date") || strings.Contains(lower, "calendar"):
		return "calendar"
	case strings.Contains(lower, "color"):
		return "palette"
	case fieldType == "color":
		return "palette"
	case fieldType == "bool":
		return "toggle-on"
	default:
		return "gear"
	}
}
