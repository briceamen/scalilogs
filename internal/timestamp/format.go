package timestamp

import (
	"strings"
	"time"
)

// Helper function to validate time format
func isValidTimeFormat(timeStr string) bool {
	timeStr = strings.TrimSpace(timeStr)
	_, err := time.Parse("15:04:05", timeStr)
	if err == nil {
		return true
	}
	// Try alternative format with just hours and minutes
	_, err = time.Parse("15:04", timeStr)
	return err == nil
}

// Helper function to ensure time format includes seconds
func normalizeTimeFormat(timeStr string) string {
	timeStr = strings.TrimSpace(timeStr)
	if strings.Count(timeStr, ":") == 1 {
		// Only has hours and minutes, add seconds
		return timeStr + ":00"
	}
	return timeStr
}

// Helper function to convert weekday string to time.Weekday
func getWeekdayFromString(weekday string) time.Weekday {
	weekday = strings.ToLower(weekday)
	switch weekday {
	case "sunday":
		return time.Sunday
	case "monday":
		return time.Monday
	case "tuesday":
		return time.Tuesday
	case "wednesday":
		return time.Wednesday
	case "thursday":
		return time.Thursday
	case "friday":
		return time.Friday
	case "saturday":
		return time.Saturday
	}
	return time.Sunday // Default fallback
}
