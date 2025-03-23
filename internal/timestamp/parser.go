package timestamp

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Scalingo/go-utils/errors/v2"
)

const (
	dateFormat     = "2006-01-02"
	timeFormat     = "15:04:05"
	dateTimeFormat = "2006-01-02 15:04:05"
)

// ValidateAndNormalize validates and normalizes various timestamp formats
// Returns a normalized timestamp in the format "YYYY-MM-DD HH:MM:SS"
func ValidateAndNormalize(ctx context.Context, input string) (string, error) {
	// Early return for empty input
	if strings.TrimSpace(input) == "" {
		return "", errors.New(ctx, "timestamp cannot be empty")
	}

	input = strings.TrimSpace(input)
	lowercaseInput := strings.ToLower(input)

	// Check for standard datetime format first
	if t, err := time.Parse(dateTimeFormat, input); err == nil {
		return t.Format(dateTimeFormat), nil
	}

	// 1. Check for "today at HH:MM:SS" format
	todayRegex := regexp.MustCompile(`(?i)^today\s+at\s+(\d{1,2}:\d{1,2}(:\d{1,2})?)$`)
	if matches := todayRegex.FindStringSubmatch(lowercaseInput); len(matches) > 1 {
		timeStr := matches[1]
		if !isValidTimeFormat(timeStr) {
			return "", errors.New(ctx, fmt.Sprintf("invalid time format '%s', please use HH:MM:SS or HH:MM", timeStr))
		}
		today := time.Now().Format(dateFormat)
		return fmt.Sprintf("%s %s", today, normalizeTimeFormat(timeStr)), nil
	}

	// 2. Check for "yesterday at HH:MM:SS" format
	yesterdayRegex := regexp.MustCompile(`(?i)^yesterday\s+at\s+(\d{1,2}:\d{1,2}(:\d{1,2})?)$`)
	if matches := yesterdayRegex.FindStringSubmatch(lowercaseInput); len(matches) > 1 {
		timeStr := matches[1]
		if !isValidTimeFormat(timeStr) {
			return "", errors.New(ctx, fmt.Sprintf("invalid time format '%s', please use HH:MM:SS or HH:MM", timeStr))
		}
		yesterday := time.Now().AddDate(0, 0, -1).Format(dateFormat)
		return fmt.Sprintf("%s %s", yesterday, normalizeTimeFormat(timeStr)), nil
	}

	// 3. Check for weekday format (e.g., "Monday at HH:MM:SS")
	weekdayRegex := regexp.MustCompile(`(?i)^(monday|tuesday|wednesday|thursday|friday|saturday|sunday)\s+at\s+(\d{1,2}:\d{1,2}(:\d{1,2})?)$`)
	if matches := weekdayRegex.FindStringSubmatch(lowercaseInput); len(matches) > 2 {
		weekday := matches[1]
		timeStr := matches[2]

		if !isValidTimeFormat(timeStr) {
			return "", errors.New(ctx, fmt.Sprintf("invalid time format '%s', please use HH:MM:SS or HH:MM", timeStr))
		}

		// Find the date for the most recent occurrence of this weekday
		targetWeekday := getWeekdayFromString(weekday)
		now := time.Now()
		daysToSubtract := (int(now.Weekday()) - int(targetWeekday) + 7) % 7
		if daysToSubtract == 0 {
			// If today is the target weekday, use 7 days ago
			daysToSubtract = 7
		}

		targetDate := now.AddDate(0, 0, -daysToSubtract).Format(dateFormat)
		return fmt.Sprintf("%s %s", targetDate, normalizeTimeFormat(timeStr)), nil
	}

	// NEW: Check for just a day name (e.g., "Monday") - default to noon
	dayOnlyRegex := regexp.MustCompile(`(?i)^(monday|tuesday|wednesday|thursday|friday|saturday|sunday)$`)
	if matches := dayOnlyRegex.FindStringSubmatch(lowercaseInput); len(matches) > 0 {
		weekday := matches[1]
		targetWeekday := getWeekdayFromString(weekday)
		now := time.Now()
		daysToSubtract := (int(now.Weekday()) - int(targetWeekday) + 7) % 7
		if daysToSubtract == 0 {
			// If today is the target weekday, use 7 days ago
			daysToSubtract = 7
		}

		targetDate := now.AddDate(0, 0, -daysToSubtract).Format(dateFormat)
		return fmt.Sprintf("%s 12:00:00", targetDate), nil
	}

	// NEW: Check for just "today" - default to noon
	if lowercaseInput == "today" {
		today := time.Now().Format(dateFormat)
		return fmt.Sprintf("%s 12:00:00", today), nil
	}

	// NEW: Check for just "yesterday" - default to noon
	if lowercaseInput == "yesterday" {
		yesterday := time.Now().AddDate(0, 0, -1).Format(dateFormat)
		return fmt.Sprintf("%s 12:00:00", yesterday), nil
	}

	// NEW: Check for "now" - use current time
	if lowercaseInput == "now" {
		return time.Now().Format(dateTimeFormat), nil
	}

	// NEW: Check for just a time value without full timestamp
	// 1. Handle just "HH" format (e.g., "12" -> "today at 12:00:00")
	hourOnlyRegex := regexp.MustCompile(`^(\d{1,2})$`)
	if matches := hourOnlyRegex.FindStringSubmatch(input); len(matches) > 0 {
		hourStr := matches[1]
		hour, err := strconv.Atoi(hourStr)
		if err == nil && hour >= 0 && hour <= 23 {
			today := time.Now().Format(dateFormat)
			return fmt.Sprintf("%s %02d:00:00", today, hour), nil
		}
	}

	// NEW: Handle "at HH" format (e.g., "at 12" -> "today at 12:00:00")
	atHourRegex := regexp.MustCompile(`(?i)^at\s+(\d{1,2})$`)
	if matches := atHourRegex.FindStringSubmatch(lowercaseInput); len(matches) > 0 {
		hourStr := matches[1]
		hour, err := strconv.Atoi(hourStr)
		if err == nil && hour >= 0 && hour <= 23 {
			today := time.Now().Format(dateFormat)
			return fmt.Sprintf("%s %02d:00:00", today, hour), nil
		}
	}

	// NEW: Handle days with time but missing "at" (e.g., "Yesterday 12")
	dayTimeNoAtRegex := regexp.MustCompile(`(?i)^(yesterday|today)\s+(\d{1,2})$`)
	if matches := dayTimeNoAtRegex.FindStringSubmatch(lowercaseInput); len(matches) > 1 {
		day := matches[1]
		hourStr := matches[2]
		hour, err := strconv.Atoi(hourStr)
		if err == nil && hour >= 0 && hour <= 23 {
			var dateStr string
			if day == "today" {
				dateStr = time.Now().Format(dateFormat)
			} else {
				dateStr = time.Now().AddDate(0, 0, -1).Format(dateFormat)
			}
			return fmt.Sprintf("%s %02d:00:00", dateStr, hour), nil
		}
	}

	// 4. Try other commonly used date formats
	otherFormats := []string{
		"2006/01/02 15:04:05",
		"2006/01/02 15:04",
		"02/01/2006 15:04:05", // DD/MM/YYYY
		"01/02/2006 15:04:05", // MM/DD/YYYY
		"2006-01-02T15:04:05",
		"2006-01-02 15:04",
	}

	for _, format := range otherFormats {
		if t, err := time.Parse(format, input); err == nil {
			return t.Format(dateTimeFormat), nil
		}
	}

	// Handle date and time without "at" (e.g., "2025-03-22 12")
	dateAtTimeRegex := regexp.MustCompile(`(?i)^(\d{4}-\d{2}-\d{2})\s+(\d{1,2}(:\d{1,2}(:\d{1,2})?)?)$`)
	if matches := dateAtTimeRegex.FindStringSubmatch(input); len(matches) > 1 {
		dateStr := matches[1]
		timeStr := matches[2]

		// Validate the date part
		_, err := time.Parse(dateFormat, dateStr)
		if err != nil {
			return "", errors.Wrap(ctx, err, "parse date part")
		}

		// Handle different time formats
		if strings.Count(timeStr, ":") == 0 {
			// Just hour (e.g., "12")
			hour, err := strconv.Atoi(timeStr)
			if err != nil || hour < 0 || hour > 23 {
				return "", errors.New(ctx, fmt.Sprintf("invalid hour: %s", timeStr))
			}
			return fmt.Sprintf("%s %02d:00:00", dateStr, hour), nil
		} else if !isValidTimeFormat(timeStr) {
			return "", errors.New(ctx, fmt.Sprintf("invalid time format: %s", timeStr))
		}

		// If we get here, the time format is valid, normalize it
		return fmt.Sprintf("%s %s", dateStr, normalizeTimeFormat(timeStr)), nil
	}

	// If we get here, the format wasn't recognized
	return "", errors.New(ctx, fmt.Sprintf("unrecognized timestamp format: %s", input))
}

var (
	standardScalingoRegex   = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}\.\d+ [+-]\d{4} \w+)`)
	routerRegex             = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}\.\d+ [+-]\d{4} \w+) \[router\]`)
	jsonTimeRegex           = regexp.MustCompile(`"time":"(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d+[+-]\d{2}:\d{2})"`)
	noTimezoneRegex         = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}\.\d+)`)
	isoRegex                = regexp.MustCompile(`(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z)`)
	standardDateTimeRegex   = regexp.MustCompile(`(\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2})`)
	bracketedTimestampRegex = regexp.MustCompile(`\[(\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2})\]`)
	appLogRegex             = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}\.\d+) app\[\w+\.\d+\]:`)
	rfc3339Regex            = regexp.MustCompile(`(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}[+-]\d{2}:\d{2})`)
)

// Parse parses a timestamp from a log line
func Parse(ctx context.Context, line string) (time.Time, error) {
	// First, try the most common formats with compiled regexes

	// Standard Scalingo format: "2025-03-18 12:32:19.860718558 +0100 CET"
	if matches := standardScalingoRegex.FindStringSubmatch(line); len(matches) > 0 {
		timestamp, err := time.Parse("2006-01-02 15:04:05.999999999 -0700 MST", matches[1])
		if err == nil {
			return timestamp, nil
		}
	}

	// Router format: "2025-02-28 07:41:00.432084040 +0100 CET [router] method=POST"
	if matches := routerRegex.FindStringSubmatch(line); len(matches) > 0 {
		timestamp, err := time.Parse("2006-01-02 15:04:05.999999999 -0700 MST", matches[1])
		if err == nil {
			return timestamp, nil
		}
	}

	// JSON time format: "time":"2025-02-28T13:00:01.690+00:00"
	if matches := jsonTimeRegex.FindStringSubmatch(line); len(matches) > 0 {
		timestamp, err := time.Parse("2006-01-02T15:04:05.999999999-07:00", matches[1])
		if err == nil {
			return timestamp, nil
		}
	}

	// Format without timezone: "2025-03-18 12:32:19.860718558"
	if matches := noTimezoneRegex.FindStringSubmatch(line); len(matches) > 0 {
		timestamp, err := time.Parse("2006-01-02 15:04:05.999999999", matches[1])
		if err == nil {
			return timestamp, nil
		}
	}

	// ISO8601 format: "2025-03-18T12:32:19Z"
	if matches := isoRegex.FindStringSubmatch(line); len(matches) > 0 {
		timestamp, err := time.Parse("2006-01-02T15:04:05Z", matches[1])
		if err == nil {
			return timestamp, nil
		}
	}

	// Standard date-time format: "2025-03-18 12:32:19"
	if matches := standardDateTimeRegex.FindStringSubmatch(line); len(matches) > 0 {
		timestamp, err := time.Parse("2006-01-02 15:04:05", matches[1])
		if err == nil {
			return timestamp, nil
		}
	}

	// Log format with bracketed timestamp: "[2025-03-18 12:32:19]"
	if matches := bracketedTimestampRegex.FindStringSubmatch(line); len(matches) > 0 {
		timestamp, err := time.Parse("2006-01-02 15:04:05", matches[1])
		if err == nil {
			return timestamp, nil
		}
	}

	// Scalingo log format with app name: "2025-03-18 12:32:19.123456 app[web.1]:"
	if matches := appLogRegex.FindStringSubmatch(line); len(matches) > 0 {
		timestamp, err := time.Parse("2006-01-02 15:04:05.999999", matches[1])
		if err == nil {
			return timestamp, nil
		}
	}

	// RFC3339 format: "2025-03-18T12:32:19+01:00"
	if matches := rfc3339Regex.FindStringSubmatch(line); len(matches) > 0 {
		timestamp, err := time.Parse(time.RFC3339, matches[1])
		if err == nil {
			return timestamp, nil
		}
	}

	// Try to extract date and time components separately if standard formats fail
	var dateStr, timeStr string
	words := strings.Fields(line)

	// Fast path to check for date and time patterns
	for _, word := range words {
		word = strings.Trim(word, "[](){},;:\"'")

		// Check for date pattern: YYYY-MM-DD
		if len(word) == 10 && word[4] == '-' && word[7] == '-' {
			_, err := time.Parse("2006-01-02", word)
			if err == nil {
				dateStr = word
				continue
			}
		}

		// Check for time pattern: HH:MM:SS
		if len(word) == 8 && word[2] == ':' && word[5] == ':' {
			_, err := time.Parse("15:04:05", word)
			if err == nil {
				timeStr = word
				continue
			}
		}
	}

	if dateStr != "" && timeStr != "" {
		timestampStr := fmt.Sprintf("%s %s", dateStr, timeStr)
		timestamp, err := time.Parse("2006-01-02 15:04:05", timestampStr)
		if err == nil {
			return timestamp, nil
		}
	}

	return time.Time{}, errors.New(ctx, "timestamp not found in log line")
}

// ParseSearch parses a timestamp from a search query
func ParseSearch(ctx context.Context, timestampStr string) (time.Time, error) {
	normalized, err := ValidateAndNormalize(ctx, timestampStr)
	if err != nil {
		return time.Time{}, err
	}

	// Parse the normalized timestamp
	t, err := time.Parse(dateTimeFormat, normalized)
	if err != nil {
		return time.Time{}, errors.Wrap(ctx, err, "parse normalized timestamp")
	}

	// Check if the input didn't specify a timezone
	if normalized == t.Format(dateTimeFormat) {
		// Try to load Europe/Paris location (commonly used in Scalingo logs)
		loc, err := time.LoadLocation("Europe/Paris")
		if err != nil {
			// Fallback to local timezone if location loading fails
			loc = time.Local
		}

		// Create a new time value in the target timezone to properly handle DST
		t = time.Date(
			t.Year(), t.Month(), t.Day(),
			t.Hour(), t.Minute(), t.Second(), t.Nanosecond(),
			loc,
		)
	}

	return t, nil
}
