package server

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"log/slog"
	"reflect"
	"slices"
	"strings"
	"time"

	"tronbyt-server/internal/data"

	"github.com/google/uuid"
	"github.com/nicksnyder/go-i18n/v2/i18n"
	"github.com/sumup/aaguids-go"
)

func getFuncMap() template.FuncMap {
	return template.FuncMap{
		"seq":           tmplSeq,
		"dict":          tmplDict,
		"timeago":       tmplTimeAgo,
		"timesince":     tmplTimeSince,
		"duration":      tmplDuration,
		"t":             tmplT,
		"deref":         tmplDeref,
		"derefOr":       tmplDerefOr,
		"isPinned":      tmplIsPinned,
		"json":          tmplJSON,
		"string":        tmplString,
		"substr":        tmplSubstr,
		"split":         strings.Split,
		"trim":          strings.TrimSpace,
		"slice":         tmplSlice,
		"contains":      tmplContains,
		"webauthn_icon": tmplWebAuthnIcon,
	}
}

func tmplSeq(start, end int) []int {
	var s []int
	for i := start; i <= end; i++ {
		s = append(s, i)
	}
	return s
}

func tmplDict(values ...any) (map[string]any, error) {
	if len(values)%2 != 0 {
		return nil, fmt.Errorf("dict expects even number of arguments")
	}
	dict := make(map[string]any, len(values)/2)
	for i := 0; i < len(values); i += 2 {
		key, ok := values[i].(string)
		if !ok {
			return nil, fmt.Errorf("dict keys must be strings")
		}
		dict[key] = values[i+1]
	}
	return dict, nil
}

func tmplTimeAgo(localizer *i18n.Localizer, ts any) string {
	return humanizeTime(localizer, ts, "TimeAgo")
}

func tmplTimeSince(localizer *i18n.Localizer, ts any) string {
	return humanizeTime(localizer, ts, "TimeSince")
}

func humanizeTime(localizer *i18n.Localizer, ts any, prefix string) string {
	var t time.Time
	switch v := ts.(type) {
	case int64:
		if v == 0 {
			return tmplT(localizer, "Never")
		}
		t = time.Unix(v, 0)
	case time.Time:
		// Check for both Go's zero time and Unix epoch (SQLite might store 0 as 1970-01-01)
		if v.IsZero() || v.Year() <= 1970 {
			return tmplT(localizer, "Never")
		}
		t = v
	case *time.Time:
		if v == nil || v.IsZero() || v.Year() <= 1970 {
			return tmplT(localizer, "Never")
		}
		t = *v
	default:
		return tmplT(localizer, "Never")
	}

	d := time.Since(t)
	if d < 0 {
		d = -d
	}

	if d < time.Minute {
		return tmplT(localizer, prefix+"_JustNow")
	}
	if d < time.Hour {
		minutes := int(d.Minutes())
		return tmplT(localizer, prefix+"_Minutes", minutes)
	}
	if d < 24*time.Hour {
		hours := int(d.Hours())
		return tmplT(localizer, prefix+"_Hours", hours)
	}
	if d < 7*24*time.Hour {
		days := int(d.Hours() / 24)
		return tmplT(localizer, prefix+"_Days", days)
	}
	if d < 30*24*time.Hour {
		weeks := int(d.Hours() / (24 * 7))
		return tmplT(localizer, prefix+"_Weeks", weeks)
	}
	if d < 365*24*time.Hour {
		months := int(d.Hours() / (24 * 30))
		return tmplT(localizer, prefix+"_Months", months)
	}
	years := int(d.Hours() / (24 * 365))
	return tmplT(localizer, prefix+"_Years", years)
}

func tmplDuration(d any) string {
	var dur time.Duration
	switch v := d.(type) {
	case int64:
		dur = time.Duration(v)
	case time.Duration:
		dur = v
	default:
		return "0s"
	}

	if dur.Seconds() < 60 {
		return fmt.Sprintf("%.3f s", dur.Seconds())
	}
	return dur.String()
}

func tmplT(localizer *i18n.Localizer, messageID string, args ...any) string {
	localizeConfig := &i18n.LocalizeConfig{
		MessageID: messageID,
		DefaultMessage: &i18n.Message{
			ID:    messageID,
			Other: messageID,
		},
	}
	if len(args) > 0 {
		if num, ok := args[0].(int); ok {
			localizeConfig.PluralCount = num
		} else if dataMap, ok := args[0].(map[string]any); ok {
			localizeConfig.TemplateData = dataMap
		}
	}
	translated, err := localizer.Localize(localizeConfig)
	if err != nil {
		slog.Warn("Translation not found", "id", messageID, "error", err)
		return messageID // Fallback to message ID (which is the English string here)
	}
	return translated
}

func tmplDeref(v any) any {
	return tmplDerefOr(v, nil)
}

func tmplDerefOr(v any, def any) any {
	if v == nil {
		return def
	}

	rv := reflect.ValueOf(v)
	for rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return def
		}
		rv = rv.Elem()
	}

	return rv.Interface()
}

func tmplIsPinned(device data.Device, iname string) bool {
	if device.PinnedApp == nil {
		return false
	}
	return *device.PinnedApp == iname
}

func tmplJSON(v any) (template.JS, error) {
	a, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return template.JS(a), nil
}

func tmplString(v any) string {
	return fmt.Sprintf("%v", v)
}

func tmplSubstr(s string, start, length int) string {
	if start < 0 {
		start = 0
	}
	if length < 0 {
		length = 0
	}
	end := min(start+length, len(s))
	if start > len(s) {
		return ""
	}
	return s[start:end]
}

func tmplSlice(args ...string) []string {
	return args
}

func tmplContains(slice []string, item string) bool {
	return slices.Contains(slice, item)
}

func tmplWebAuthnIcon(authenticator string, dark bool) template.URL {
	aaguidBytes, err := hex.DecodeString(authenticator)
	if err != nil {
		slog.Debug("tmplWebAuthnIcon: failed to decode authenticator hex string", "authenticator", authenticator, "error", err)
		return ""
	}

	id, err := uuid.FromBytes(aaguidBytes)
	if err != nil {
		slog.Debug("tmplWebAuthnIcon: failed to create uuid from bytes", "authenticator", authenticator, "error", err)
		return ""
	}

	metadata, err := aaguids.GetMetadata(id.String())
	if err != nil {
		slog.Debug("tmplWebAuthnIcon: failed to get metadata", "uuid", id.String(), "error", err)
		return ""
	}
	if metadata == nil {
		return ""
	}

	if dark {
		return template.URL(metadata.IconDark)
	}
	return template.URL(metadata.IconLight)
}
