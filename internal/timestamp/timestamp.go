// Package timestamp parses and formats Slack message timestamps and CLI time bounds.
package timestamp

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ParseSlack converts a Slack timestamp (seconds or seconds.nanoseconds) into UTC time.
func ParseSlack(ts string) (time.Time, error) {
	parts := strings.SplitN(ts, ".", 2)
	if len(parts) == 0 || parts[0] == "" {
		return time.Time{}, errors.New("invalid slack timestamp")
	}
	secs, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return time.Time{}, err
	}
	var nsecs int64
	if len(parts) > 1 {
		fractional := parts[1]
		if len(fractional) > 9 {
			fractional = fractional[:9]
		}
		for len(fractional) < 9 {
			fractional += "0"
		}
		nsecs, err = strconv.ParseInt(fractional, 10, 64)
		if err != nil {
			return time.Time{}, err
		}
	}
	return time.Unix(secs, nsecs).UTC(), nil
}

// FormatSlack converts a time into a Slack timestamp string.
func FormatSlack(t time.Time) string {
	secs := t.Unix()
	nsecs := t.Nanosecond()
	if nsecs == 0 {
		return strconv.FormatInt(secs, 10)
	}
	return fmt.Sprintf("%d.%09d", secs, nsecs)
}

// FormatMessage converts a Slack timestamp into RFC3339 UTC, or returns the raw value on error.
func FormatMessage(ts string) string {
	t, err := ParseSlack(ts)
	if err != nil {
		return ts
	}
	return t.UTC().Format(time.RFC3339)
}

// ParseBound converts a CLI time bound into a Slack timestamp string (seconds.nanoseconds).
// endOfDay is used for date-only values so -to 2024-05-01 includes the whole day.
func ParseBound(value string, endOfDay bool) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}

	if t, err := time.Parse(time.RFC3339, value); err == nil {
		return FormatSlack(t.UTC()), nil
	}
	if t, err := time.ParseInLocation("2006-01-02", value, time.UTC); err == nil {
		if endOfDay {
			t = t.Add(24*time.Hour - time.Nanosecond)
		}
		return FormatSlack(t.UTC()), nil
	}
	if !endOfDay {
		if d, err := parseRelativeDuration(value); err == nil {
			return FormatSlack(time.Now().UTC().Add(-d)), nil
		}
	}
	return "", fmt.Errorf("use RFC3339, YYYY-MM-DD%s", relativeHint(endOfDay))
}

// FormatBoundForLog renders a CLI time bound for progress output.
func FormatBoundForLog(raw, slackTS string) string {
	if raw == "" {
		return "(none)"
	}
	if slackTS == "" {
		return raw
	}
	t, err := ParseSlack(slackTS)
	if err != nil {
		return raw
	}
	return fmt.Sprintf("%s (%s)", raw, t.UTC().Format(time.RFC3339))
}

func relativeHint(endOfDay bool) string {
	if endOfDay {
		return ""
	}
	return ", or a relative duration like 7d/24h"
}

func parseRelativeDuration(value string) (time.Duration, error) {
	if len(value) < 2 {
		return 0, fmt.Errorf("invalid relative duration")
	}
	suffix := value[len(value)-1]
	amountStr := value[:len(value)-1]
	amount, err := strconv.ParseFloat(amountStr, 64)
	if err != nil || amount < 0 {
		return 0, fmt.Errorf("invalid relative duration")
	}
	switch suffix {
	case 's', 'S':
		return time.Duration(amount * float64(time.Second)), nil
	case 'm', 'M':
		return time.Duration(amount * float64(time.Minute)), nil
	case 'h', 'H':
		return time.Duration(amount * float64(time.Hour)), nil
	case 'd', 'D':
		return time.Duration(amount * float64(24*time.Hour)), nil
	case 'w', 'W':
		return time.Duration(amount * float64(7*24*time.Hour)), nil
	default:
		return 0, fmt.Errorf("unsupported duration suffix %q", string(suffix))
	}
}
