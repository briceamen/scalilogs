package logs

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"runtime"
	"sort"
	"sync"
	"time"

	"github.com/Scalingo/go-utils/errors/v2"
	"github.com/briceamen/logaround/internal/timestamp"
)

// LogLine represents a single log line with its timestamp
type LogLine struct {
	Timestamp time.Time
	Content   string
	Index     int // Store original line index for preserving ordering
}

// FilterByTimestamp filters log lines around a specific timestamp, keeping a certain number of lines before and after
func FilterByTimestamp(ctx context.Context, inputFile, outputFile, targetTimestampStr string, lineCount int, appName string) (int, error) {
	fmt.Printf("Filtering logs around timestamp: %s (±%d lines)...\n", targetTimestampStr, lineCount)

	// Parse target timestamp
	targetTimestamp, err := timestamp.ParseSearch(ctx, targetTimestampStr)
	if err != nil {
		return 0, errors.Wrap(ctx, err, "parse target timestamp")
	}

	// Read all lines into memory first (faster for parallel processing)
	file, err := os.Open(inputFile)
	if err != nil {
		return 0, errors.Wrap(ctx, err, "open input file for filtering")
	}
	defer file.Close()

	var lines []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}

	if err := scanner.Err(); err != nil {
		return 0, errors.Wrap(ctx, err, "read input file for filtering")
	}

	if len(lines) == 0 {
		return 0, errors.New(ctx, "no log lines were read from the input file")
	}

	// Process lines in parallel
	numWorkers := runtime.NumCPU()
	var wg sync.WaitGroup

	// Variables to track the closest timestamp across all goroutines
	var globalMutex sync.Mutex
	var closestIndex int = -1
	var closestTimeDiff time.Duration = time.Hour * 24 * 365 // Start with a large value (1 year)

	// Create logLines slice with proper capacity
	logLines := make([]LogLine, len(lines))

	// Calculate chunk size for worker distribution
	chunkSize := len(lines) / numWorkers
	if chunkSize == 0 {
		chunkSize = 1
	}

	// Launch workers to process chunks in parallel
	for w := 0; w < numWorkers; w++ {
		wg.Add(1)

		startIdx := w * chunkSize
		endIdx := (w + 1) * chunkSize
		if w == numWorkers-1 || endIdx > len(lines) {
			endIdx = len(lines)
		}

		go func(start, end int) {
			defer wg.Done()

			// Process each line in the chunk
			for i := start; i < end; i++ {
				line := lines[i]
				ts, err := timestamp.Parse(ctx, line)

				// Store the parsed line
				logLines[i] = LogLine{
					Timestamp: ts,
					Content:   line,
					Index:     i,
				}

				// If we successfully parsed a timestamp, check if it's closest to target
				if err == nil {
					timeDiff := ts.Sub(targetTimestamp)
					if timeDiff < 0 {
						timeDiff = -timeDiff // Get absolute value
					}

					// Update the closest timestamp info (thread-safe)
					globalMutex.Lock()
					if timeDiff < closestTimeDiff {
						closestTimeDiff = timeDiff
						closestIndex = i
					}
					globalMutex.Unlock()
				}
			}
		}(startIdx, endIdx)
	}

	// Wait for all workers to complete
	wg.Wait()

	if closestIndex == -1 {
		return 0, errors.New(ctx, "could not find any log lines with timestamps")
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
		return 0, errors.Wrap(ctx, err, "create filtered output file")
	}
	defer outFile.Close()

	// Add header with information about the filtering
	writer := bufio.NewWriter(outFile)
	fmt.Fprintf(writer, "# Log search results for app: %s\n", appName)
	fmt.Fprintf(writer, "# Target timestamp: %s\n", targetTimestampStr)
	fmt.Fprintf(writer, "# Lines before and after: %d\n", lineCount)
	fmt.Fprintf(writer, "# Closest log timestamp: %s\n", logLines[closestIndex].Timestamp.Format("2006-01-02 15:04:05.999999999 -0700 MST"))
	fmt.Fprintf(writer, "# Time difference from target: %s\n", closestTimeDiff)
	fmt.Fprintf(writer, "# Total lines extracted: %d\n", endIndex-startIndex+1)

	// Add timing information
	var minTimestamp, maxTimestamp time.Time
	if startIndex < len(logLines) && endIndex < len(logLines) {
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
		return 0, errors.Wrap(ctx, err, "flush filtered output file")
	}

	fmt.Printf("Found closest timestamp at %s (difference: %s)\n",
		logLines[closestIndex].Timestamp.Format("2006-01-02 15:04:05"),
		closestTimeDiff)
	fmt.Printf("Extracted %d lines around the target time\n\n", endIndex-startIndex+1)

	return endIndex - startIndex + 1, nil
}

// FilterByHours filters log lines within a specific time range around a timestamp
func FilterByHours(ctx context.Context, inputFile, outputFile, targetTimestampStr string, hoursCount int, appName string) (int, error) {
	// Parse target timestamp
	targetTimestamp, err := timestamp.ParseSearch(ctx, targetTimestampStr)
	if err != nil {
		return 0, errors.Wrap(ctx, err, "parse target timestamp")
	}

	// Calculate time boundaries
	startTime := targetTimestamp.Add(-time.Duration(hoursCount) * time.Hour)
	endTime := targetTimestamp.Add(time.Duration(hoursCount) * time.Hour)

	// Read all lines into memory first (faster for parallel processing)
	file, err := os.Open(inputFile)
	if err != nil {
		return 0, errors.Wrap(ctx, err, "open input file for filtering")
	}
	defer file.Close()

	var lines []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}

	if err := scanner.Err(); err != nil {
		return 0, errors.Wrap(ctx, err, "read input file for filtering")
	}

	if len(lines) == 0 {
		return 0, errors.New(ctx, "no log lines were read from the input file")
	}

	// Process in parallel
	numWorkers := runtime.NumCPU()
	totalLogLines := len(lines)

	// Create channels and sync primitives
	var wg sync.WaitGroup
	var mutex sync.Mutex // Protects shared data structures
	var filteredLogLines []LogLine
	var closestIndex int = -1
	var closestTimeDiff time.Duration = time.Hour * 24 * 365 // Start with a large value (1 year)
	var inRangeCount int32 = 0

	// Calculate chunk size for workers
	chunkSize := totalLogLines / numWorkers
	if chunkSize == 0 {
		chunkSize = 1
	}

	// Create workers to process chunks in parallel
	for w := 0; w < numWorkers; w++ {
		wg.Add(1)

		startIdx := w * chunkSize
		endIdx := (w + 1) * chunkSize
		if w == numWorkers-1 || endIdx > totalLogLines {
			endIdx = totalLogLines
		}

		go func(start, end int) {
			defer wg.Done()

			// Local filtered results for this worker
			var localFiltered []LogLine
			localClosestIndex := -1
			localClosestTimeDiff := time.Hour * 24 * 365

			// Process chunk
			for i := start; i < end; i++ {
				line := lines[i]
				ts, err := timestamp.Parse(ctx, line)

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
						Index:     i,
					}

					// Track closest log to target locally
					if timeDiff < localClosestTimeDiff {
						localClosestTimeDiff = timeDiff
						localClosestIndex = len(localFiltered)
					}

					localFiltered = append(localFiltered, logLine)
				}
			}

			// Now merge local results with global results
			if len(localFiltered) > 0 {
				mutex.Lock()

				// Update global closest timestamp info
				if localClosestIndex >= 0 && localClosestTimeDiff < closestTimeDiff {
					closestTimeDiff = localClosestTimeDiff
					closestIndex = len(filteredLogLines) + localClosestIndex
				}

				// Append all local filtered lines to global results
				filteredLogLines = append(filteredLogLines, localFiltered...)
				inRangeCount += int32(len(localFiltered))

				mutex.Unlock()
			}
		}(startIdx, endIdx)
	}

	// Wait for all workers to finish
	wg.Wait()

	if len(filteredLogLines) == 0 {
		return 0, errors.New(ctx, "could not find any log entries within specified time range")
	}

	// Sort by timestamp to ensure chronological order
	sort.Slice(filteredLogLines, func(i, j int) bool {
		return filteredLogLines[i].Timestamp.Before(filteredLogLines[j].Timestamp)
	})

	// Write filtered lines to output file
	outFile, err := os.Create(outputFile)
	if err != nil {
		return 0, errors.Wrap(ctx, err, "create filtered output file")
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
	fmt.Fprintf(writer, "# Total lines in range: %d of %d total logs\n", len(filteredLogLines), totalLogLines)

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

		// Sort slice is done earlier in the function now

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
			} else if timeDiff > largeGapThreshold {
				gapDetected = true
			}
		}

		// Report significant gaps first
		if len(significantGaps) > 0 {
			fmt.Fprintf(writer, "# WARNING: %d significant gaps detected in log coverage:\n", len(significantGaps))
			for i, gap := range significantGaps {
				fmt.Fprintf(writer, "#  %d. Gap of %s between %s and %s\n",
					i+1,
					gap.Duration.Round(time.Second),
					gap.Start.Format("2006-01-02 15:04:05"),
					gap.End.Format("2006-01-02 15:04:05"))
			}
		} else if gapDetected {
			fmt.Fprintf(writer, "# WARNING: Gaps of >10 minutes detected in log coverage. This may indicate missing logs.\n")
		}
	}

	fmt.Fprintf(writer, "\n")

	// Write each log line, highlighting the one closest to the target time
	for i, logLine := range filteredLogLines {
		if i == closestIndex {
			fmt.Fprintf(writer, ">>> %s <<<\n", logLine.Content)
		} else {
			fmt.Fprintf(writer, "%s\n", logLine.Content)
		}
	}

	if err := writer.Flush(); err != nil {
		return 0, errors.Wrap(ctx, err, "flush filtered output file")
	}

	fmt.Printf("Found closest timestamp at %s (difference: %s)\n",
		filteredLogLines[closestIndex].Timestamp.Format("2006-01-02 15:04:05"),
		closestTimeDiff)
	fmt.Printf("Extracted %d logs within ±%d hours of the target time\n\n",
		len(filteredLogLines), hoursCount)

	return len(filteredLogLines), nil
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
