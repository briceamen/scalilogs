package logs

import (
	"bufio"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/briceamen/logaround/internal/timestamp"
)

// FilterByTimestamp filters log lines around a specific timestamp
func FilterByTimestamp(inputFile, outputFile, targetTimestampStr string, lineCount int, appName string) error {
	fmt.Printf("Filtering logs around timestamp: %s (±%d lines)...\n", targetTimestampStr, lineCount)

	// Parse target timestamp
	targetTimestamp, err := timestamp.ParseSearch(targetTimestampStr)
	if err != nil {
		return fmt.Errorf("parse target timestamp: %w", err)
	}

	// Read all log lines
	file, err := os.Open(inputFile)
	if err != nil {
		return fmt.Errorf("open input file for filtering: %w", err)
	}
	defer file.Close()

	var logLines []LogLine
	var closestIndex int = -1
	var closestTimeDiff time.Duration = time.Hour * 24 * 365 // Start with a large value (1 year)

	scanner := bufio.NewScanner(file)
	lineIndex := 0

	// First pass: read all lines and find the closest timestamp
	for scanner.Scan() {
		line := scanner.Text()
		ts, err := timestamp.Parse(line)

		// If we can parse a timestamp, check if it's closer to our target
		if err == nil {
			timeDiff := ts.Sub(targetTimestamp)
			if timeDiff < 0 {
				timeDiff = -timeDiff // Get absolute value
			}

			if timeDiff < closestTimeDiff {
				closestTimeDiff = timeDiff
				closestIndex = lineIndex
			}
		}

		logLines = append(logLines, LogLine{
			Timestamp: ts,
			Content:   line,
		})

		lineIndex++
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read input file for filtering: %w", err)
	}

	if closestIndex == -1 {
		return fmt.Errorf("could not find any log lines with timestamps")
	}

	if len(logLines) == 0 {
		return fmt.Errorf("no log lines were read from the input file")
	}

	// Determine range to extract
	startIndex := closestIndex - lineCount
	if startIndex < 0 {
		startIndex = 0
	}

	endIndex := closestIndex + lineCount
	if endIndex >= len(logLines) {
		endIndex = len(logLines) - 1
	}

	// Write filtered lines to output file
	outFile, err := os.Create(outputFile)
	if err != nil {
		return fmt.Errorf("create filtered output file: %w", err)
	}
	defer outFile.Close()

	// Add header with information about the filtering
	writer := bufio.NewWriter(outFile)
	fmt.Fprintf(writer, "# Log search results for app: %s\n", appName)
	fmt.Fprintf(writer, "# Target timestamp: %s\n", targetTimestampStr)
	fmt.Fprintf(writer, "# Closest log timestamp: %s\n", logLines[closestIndex].Timestamp.Format("2006-01-02 15:04:05.999999999 -0700 MST"))
	fmt.Fprintf(writer, "# Time difference: %s\n", closestTimeDiff)
	fmt.Fprintf(writer, "# Showing %d lines before and after\n", lineCount)
	fmt.Fprintf(writer, "# Total lines: %d\n", endIndex-startIndex+1)

	// Add a warning about potential archive boundaries
	var minTimestamp, maxTimestamp time.Time

	// Find min and max timestamps in the filtered logs
	for i := startIndex; i <= endIndex; i++ {
		if !logLines[i].Timestamp.IsZero() {
			if minTimestamp.IsZero() || logLines[i].Timestamp.Before(minTimestamp) {
				minTimestamp = logLines[i].Timestamp
			}
			if maxTimestamp.IsZero() || logLines[i].Timestamp.After(maxTimestamp) {
				maxTimestamp = logLines[i].Timestamp
			}
		}
	}

	if !minTimestamp.IsZero() && !maxTimestamp.IsZero() {
		timeDiff := maxTimestamp.Sub(minTimestamp)
		expectedLines := int(timeDiff.Seconds()) / 5 // Assuming ~5 seconds between log lines on average
		actualLines := endIndex - startIndex + 1

		// If we have significantly fewer lines than expected based on the time span,
		// or there are large temporal jumps between consecutive logs, warn about possible gaps
		if actualLines < expectedLines/2 || hasLargeTimeJumps(logLines, startIndex, endIndex) {
			fmt.Fprintf(writer, "# WARNING: The logs cover a time span of %s but contain only %d lines.\n", timeDiff, actualLines)
			fmt.Fprintf(writer, "# This may indicate gaps in the log coverage, possibly due to archive boundaries.\n")
		}
	}

	fmt.Fprintf(writer, "\n")

	// Write the filtered lines
	for i := startIndex; i <= endIndex; i++ {
		if i == closestIndex {
			fmt.Fprintf(writer, ">>> %s <<<\n", logLines[i].Content)
		} else {
			fmt.Fprintf(writer, "%s\n", logLines[i].Content)
		}
	}

	if err := writer.Flush(); err != nil {
		return fmt.Errorf("flush filtered output file: %w", err)
	}

	fmt.Printf("Found closest timestamp at %s (difference: %s)\n",
		logLines[closestIndex].Timestamp.Format("2006-01-02 15:04:05"),
		closestTimeDiff)
	fmt.Printf("Extracted %d lines around the target time\n", endIndex-startIndex+1)

	return nil
}

// FilterByHours filters log lines within a specific time range around a timestamp
func FilterByHours(inputFile, outputFile, targetTimestampStr string, hoursCount int, appName string) error {
	// This print statement is now handled by the caller in extract.go
	// fmt.Printf("Filtering logs around timestamp: %s (±%d hours)...\n", targetTimestampStr, hoursCount)

	// Parse target timestamp
	targetTimestamp, err := timestamp.ParseSearch(targetTimestampStr)
	if err != nil {
		return fmt.Errorf("parse target timestamp: %w", err)
	}

	// Calculate time boundaries
	startTime := targetTimestamp.Add(-time.Duration(hoursCount) * time.Hour)
	endTime := targetTimestamp.Add(time.Duration(hoursCount) * time.Hour)

	// Read all log lines
	file, err := os.Open(inputFile)
	if err != nil {
		return fmt.Errorf("open input file for filtering: %w", err)
	}
	defer file.Close()

	var filteredLogLines []LogLine
	var totalLogLines int
	var inRangeLogLines int
	var closestIndex int = -1
	var closestTimeDiff time.Duration = time.Hour * 24 * 365 // Start with a large value (1 year)

	scanner := bufio.NewScanner(file)

	// Read all lines and filter by time range
	for scanner.Scan() {
		line := scanner.Text()
		ts, err := timestamp.Parse(line)
		totalLogLines++

		// Skip lines without valid timestamps
		if err != nil {
			continue
		}

		// Calculate time difference from target for finding closest log
		timeDiff := ts.Sub(targetTimestamp)
		if timeDiff < 0 {
			timeDiff = -timeDiff // Get absolute value
		}

		// Check if this log is within our time range (inclusively)
		if (ts.After(startTime) || ts.Equal(startTime)) && (ts.Before(endTime) || ts.Equal(endTime)) {
			logLine := LogLine{
				Timestamp: ts,
				Content:   line,
			}

			// Keep track of the closest log to the target timestamp
			if timeDiff < closestTimeDiff {
				closestTimeDiff = timeDiff
				closestIndex = len(filteredLogLines)
			}

			filteredLogLines = append(filteredLogLines, logLine)
			inRangeLogLines++
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read input file for filtering: %w", err)
	}

	if len(filteredLogLines) == 0 {
		return fmt.Errorf("could not find any log entries within specified time range")
	}

	// Write filtered lines to output file
	outFile, err := os.Create(outputFile)
	if err != nil {
		return fmt.Errorf("create filtered output file: %w", err)
	}
	defer outFile.Close()

	// Add header with information about the filtering
	writer := bufio.NewWriter(outFile)
	fmt.Fprintf(writer, "# Log search results for app: %s\n", appName)
	fmt.Fprintf(writer, "# Target timestamp: %s\n", targetTimestampStr)
	fmt.Fprintf(writer, "# Time range: %s to %s\n", startTime.Format("2006-01-02 15:04:05"), endTime.Format("2006-01-02 15:04:05"))
	fmt.Fprintf(writer, "# Hours before and after: %d\n", hoursCount)
	fmt.Fprintf(writer, "# Closest log timestamp: %s\n", filteredLogLines[closestIndex].Timestamp.Format("2006-01-02 15:04:05.999999999 -0700 MST"))
	fmt.Fprintf(writer, "# Time difference from target: %s\n", closestTimeDiff)
	fmt.Fprintf(writer, "# Total lines in range: %d of %d total logs\n", inRangeLogLines, totalLogLines)

	// Add a warning if we see sparse log coverage
	if len(filteredLogLines) > 0 {
		totalTimeRange := time.Duration(hoursCount*2) * time.Hour
		averageLinesPerHour := float64(len(filteredLogLines)) / totalTimeRange.Hours()

		if averageLinesPerHour < 10 {
			fmt.Fprintf(writer, "# WARNING: Log density is low (%.1f lines/hour). This may indicate gaps in coverage.\n", averageLinesPerHour)
		}

		// Check for large time jumps between consecutive logs
		gapDetected := false
		largeGapThreshold := 10 * time.Minute
		veryLargeGapThreshold := 30 * time.Minute

		// Sort by timestamp to ensure chronological order
		sort.Slice(filteredLogLines, func(i, j int) bool {
			return filteredLogLines[i].Timestamp.Before(filteredLogLines[j].Timestamp)
		})

		// First check for very large gaps to highlight separately
		var significantGaps []struct {
			Start    time.Time
			End      time.Time
			Duration time.Duration
		}

		for i := 1; i < len(filteredLogLines); i++ {
			timeDiff := filteredLogLines[i].Timestamp.Sub(filteredLogLines[i-1].Timestamp)
			if timeDiff > veryLargeGapThreshold {
				significantGaps = append(significantGaps, struct {
					Start    time.Time
					End      time.Time
					Duration time.Duration
				}{
					Start:    filteredLogLines[i-1].Timestamp,
					End:      filteredLogLines[i].Timestamp,
					Duration: timeDiff,
				})
			}
		}

		// Report significant gaps first
		if len(significantGaps) > 0 {
			fmt.Fprintf(writer, "# WARNING: Significant time gaps detected in logs:\n")
			for i, gap := range significantGaps {
				fmt.Fprintf(writer, "#   - %s gap between %s and %s\n",
					gap.Duration.Round(time.Second),
					gap.Start.Format("2006-01-02 15:04:05"),
					gap.End.Format("2006-01-02 15:04:05"))

				// Determine what portion of the requested time range is missing
				gapStart := gap.Start
				gapEnd := gap.End
				timeRangeStart := targetTimestamp.Add(-time.Duration(hoursCount) * time.Hour)
				timeRangeEnd := targetTimestamp.Add(time.Duration(hoursCount) * time.Hour)

				if gapStart.After(timeRangeStart) && gapEnd.Before(timeRangeEnd) {
					totalRequestedDuration := timeRangeEnd.Sub(timeRangeStart)
					gapPercentage := (gap.Duration.Hours() / totalRequestedDuration.Hours()) * 100
					fmt.Fprintf(writer, "#     This represents approximately %.1f%% of your requested time range\n", gapPercentage)
				}

				if i >= 2 { // Limit to 3 most significant gaps
					if len(significantGaps) > 3 {
						fmt.Fprintf(writer, "#   (additional %d significant gaps exist...)\n", len(significantGaps)-3)
					}
					break
				}
			}
		}

		// Then check for smaller gaps
		smallGapsCount := 0
		for i := 1; i < len(filteredLogLines); i++ {
			timeDiff := filteredLogLines[i].Timestamp.Sub(filteredLogLines[i-1].Timestamp)
			if timeDiff > largeGapThreshold && timeDiff <= veryLargeGapThreshold {
				if !gapDetected {
					if len(significantGaps) > 0 {
						fmt.Fprintf(writer, "# Additional smaller gaps detected:\n")
					} else {
						fmt.Fprintf(writer, "# WARNING: Time gaps detected between log entries:\n")
					}
					gapDetected = true
				}

				smallGapsCount++
				if smallGapsCount <= 3 { // Only show details for the first 3 small gaps
					fmt.Fprintf(writer, "#   - %s gap between %s and %s\n",
						timeDiff.Round(time.Second),
						filteredLogLines[i-1].Timestamp.Format("15:04:05"),
						filteredLogLines[i].Timestamp.Format("15:04:05"))
				}
			}
		}

		if smallGapsCount > 3 {
			fmt.Fprintf(writer, "#   (additional %d smaller gaps exist...)\n", smallGapsCount-3)
		}

		// Check for potential missing periods around target time
		hasLogsBeforeTarget := false
		hasLogsAfterTarget := false

		for _, logLine := range filteredLogLines {
			if logLine.Timestamp.Before(targetTimestamp) {
				hasLogsBeforeTarget = true
			}
			if logLine.Timestamp.After(targetTimestamp) {
				hasLogsAfterTarget = true
			}
		}

		if !hasLogsBeforeTarget {
			fmt.Fprintf(writer, "# WARNING: No logs found before the target timestamp. This may indicate missing data.\n")
		}

		if !hasLogsAfterTarget {
			fmt.Fprintf(writer, "# WARNING: No logs found after the target timestamp. This may indicate missing data.\n")
		}
	}

	fmt.Fprintf(writer, "\n")

	// Write the filtered lines
	for i, logLine := range filteredLogLines {
		if i == closestIndex {
			fmt.Fprintf(writer, ">>> %s <<<\n", logLine.Content)
		} else {
			fmt.Fprintf(writer, "%s\n", logLine.Content)
		}
	}

	if err := writer.Flush(); err != nil {
		return fmt.Errorf("flush filtered output file: %w", err)
	}

	fmt.Printf("Found closest timestamp at %s (difference: %s)\n",
		filteredLogLines[closestIndex].Timestamp.Format("2006-01-02 15:04:05"),
		closestTimeDiff)
	fmt.Printf("Extracted %d logs within ±%d hours of the target time\n",
		len(filteredLogLines), hoursCount)

	return nil
}

// Check if there are large time jumps between consecutive log entries
func hasLargeTimeJumps(logLines []LogLine, startIndex, endIndex int) bool {
	const largeJumpThreshold = 5 * time.Minute // Define what constitutes a "large" jump

	var prevTimestamp time.Time
	for i := startIndex; i <= endIndex; i++ {
		if logLines[i].Timestamp.IsZero() {
			continue
		}

		if !prevTimestamp.IsZero() {
			timeDiff := logLines[i].Timestamp.Sub(prevTimestamp)
			if timeDiff > largeJumpThreshold || timeDiff < -largeJumpThreshold {
				return true
			}
		}

		prevTimestamp = logLines[i].Timestamp
	}

	return false
}
