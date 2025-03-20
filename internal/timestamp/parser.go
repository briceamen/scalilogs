package timestamp

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	dateFormat     = "2006-01-02"
	timeFormat     = "15:04:05"
	dateTimeFormat = "2006-01-02 15:04:05"
)

// ValidateAndNormalize validates and normalizes various timestamp formats
// Returns a normalized timestamp in the format "YYYY-MM-DD HH:MM:SS"
func ValidateAndNormalize(input string) (string, error) {
	// Early return for empty input
	if strings.TrimSpace(input) == "" {
		return "", fmt.Errorf("validate timestamp: timestamp cannot be empty")
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
			return "", fmt.Errorf("validate time format: invalid time format '%s', please use HH:MM:SS or HH:MM", timeStr)
		}
		today := time.Now().Format(dateFormat)
		return fmt.Sprintf("%s %s", today, normalizeTimeFormat(timeStr)), nil
	}

	// 2. Check for "yesterday at HH:MM:SS" format
	yesterdayRegex := regexp.MustCompile(`(?i)^yesterday\s+at\s+(\d{1,2}:\d{1,2}(:\d{1,2})?)$`)
	if matches := yesterdayRegex.FindStringSubmatch(lowercaseInput); len(matches) > 1 {
		timeStr := matches[1]
		if !isValidTimeFormat(timeStr) {
			return "", fmt.Errorf("validate time format: invalid time format '%s', please use HH:MM:SS or HH:MM", timeStr)
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
			return "", fmt.Errorf("validate time format: invalid time format '%s', please use HH:MM:SS or HH:MM", timeStr)
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

	// If we get here, the format wasn't recognized
	return "", fmt.Errorf("parse timestamp: unrecognized timestamp format: %s", input)
}

// Parse parses a timestamp from a log line
func Parse(line string) (time.Time, error) {
	// Try to extract timestamp in format "2025-03-18 12:32:19.860718558 +0100 CET"
	parts := strings.SplitN(line, " ", 4)
	if len(parts) < 3 {
		return time.Time{}, fmt.Errorf("parse log line: timestamp not found in log line")
	}

	dateStr := parts[0]
	timeStr := parts[1]

	// Check if it looks like a date
	if !strings.HasPrefix(dateStr, "20") || len(dateStr) != 10 {
		return time.Time{}, fmt.Errorf("validate date: invalid date format")
	}

	// Parse timestamp
	timestamp, err := time.Parse("2006-01-02 15:04:05.999999999 -0700 MST",
		fmt.Sprintf("%s %s %s", dateStr, timeStr, parts[2]))
	if err != nil {
		// Try alternative format
		timestamp, err = time.Parse("2006-01-02 15:04:05.999999999",
			fmt.Sprintf("%s %s", dateStr, timeStr))
		if err != nil {
			return time.Time{}, fmt.Errorf("parse timestamp: %w", err)
		}
	}

	return timestamp, nil
}

// ParseSearch parses a timestamp from a search query
func ParseSearch(timestampStr string) (time.Time, error) {
	normalized, err := ValidateAndNormalize(timestampStr)
	if err != nil {
		return time.Time{}, err
	}

	return time.Parse(dateTimeFormat, normalized)
}
