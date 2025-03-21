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
	"github.com/briceamen/scalilogs/internal/status"
	"github.com/briceamen/scalilogs/internal/timestamp"
)

// LogLine represents a single log line with its timestamp
type LogLine struct {
	Timestamp time.Time
	Content   string
	Index     int // Store original line index for preserving ordering
}

// writeFilteredLogs writes filtered log lines to an output file with appropriate headers and markers
func writeFilteredLogs(
	ctx context.Context,
	writer *bufio.Writer,
	filteredLines []LogLine,
	logLines []LogLine,
	closestIndex int,
	targetTimestampStr string,
	smallestDiff time.Duration,
	appName string,
	additionalHeaderInfo func(*bufio.Writer) error,
) error {
	// Load Europe/Paris location for consistent display
	parisLoc, err := time.LoadLocation("Europe/Paris")
	if err != nil {
		// Fallback to local timezone if location loading fails
		parisLoc = time.Local
	}

	// Parse target timestamp to display timezone info
	targetTimeObj, err := timestamp.ParseSearch(ctx, targetTimestampStr)
	if err == nil {
		// Update the display string to include timezone information
		targetTimestampStr = targetTimeObj.In(parisLoc).Format("2006-01-02 15:04:05")
	}

	// Add header with common information
	fmt.Fprintf(writer, "# Log search results for app: %s\n", appName)
	fmt.Fprintf(writer, "# Target timestamp: %s (Europe/Paris)\n", targetTimestampStr)

	// Include closest log timestamp info if available
	if closestIndex >= 0 && !logLines[closestIndex].Timestamp.IsZero() {
		// Convert closest timestamp to Paris time for display
		closestTime := logLines[closestIndex].Timestamp.In(parisLoc)
		closestTimeStr := closestTime.Format("2006-01-02 15:04:05.999999999 -0700 MST")

		fmt.Fprintf(writer, "# Closest log timestamp: %s\n", closestTimeStr)

		// Format the time difference in a more human-readable way
		diffStr := smallestDiff.String()
		if smallestDiff < time.Second {
			diffStr = fmt.Sprintf("%.3fs", smallestDiff.Seconds())
		}
		fmt.Fprintf(writer, "# Time difference: %s\n", diffStr)
	}

	// Call the function to add additional headers specific to each filter type
	if additionalHeaderInfo != nil {
		if err := additionalHeaderInfo(writer); err != nil {
			return errors.Wrap(ctx, err, "write additional header information")
		}
	}

	fmt.Fprintf(writer, "# Total lines: %d\n", len(filteredLines))
	fmt.Fprintf(writer, "\n")

	// Write the filtered lines with markers for the closest one
	closestLineIndex := -1
	if closestIndex >= 0 {
		closestLineIndex = logLines[closestIndex].Index
	}

	for _, logLine := range filteredLines {
		if logLine.Index == closestLineIndex {
			// Mark the closest log line with >>> <<<
			fmt.Fprintf(writer, ">>> %s <<<\n", logLine.Content)
		} else {
			fmt.Fprintf(writer, "%s\n", logLine.Content)
		}
	}

	return writer.Flush()
}

// FilterByTimestamp filters log lines around a specific timestamp, keeping a certain number of lines before and after
func FilterByTimestamp(ctx context.Context, inputFile, outputFile, targetTimestampStr string, lineCount int, appName string, statusCh chan<- status.StatusMessage) (int, error) {
	// Parse target timestamp
	targetTimestamp, err := timestamp.ParseSearch(ctx, targetTimestampStr)
	if err != nil {
		return 0, errors.Wrap(ctx, err, "parse target timestamp")
	}

	// Open input file
	file, err := os.Open(inputFile)
	if err != nil {
		return 0, errors.Wrap(ctx, err, "open input file for filtering")
	}
	defer file.Close()

	// Read all lines into memory
	var lines []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		// Skip empty lines
		if line == "" {
			continue
		}
		lines = append(lines, line)
	}

	if err := scanner.Err(); err != nil {
		return 0, errors.Wrap(ctx, err, "read input file for filtering")
	}

	// Process in parallel using worker pool
	numWorkers := runtime.NumCPU()
	var logLines []LogLine
	var mutex sync.Mutex

	// Create channels for work distribution
	jobs := make(chan struct {
		line  string
		index int
	}, len(lines))
	results := make(chan LogLine, len(lines))

	// Create worker pool
	var wg sync.WaitGroup
	wg.Add(numWorkers)

	// Launch workers
	for w := 0; w < numWorkers; w++ {
		go func() {
			defer wg.Done()
			for job := range jobs {
				// Try to parse timestamp from the line
				ts, _ := timestamp.Parse(ctx, job.line)
				results <- LogLine{
					Timestamp: ts,
					Content:   job.line,
					Index:     job.index,
				}
			}
		}()
	}

	// Send jobs to workers
	for i, line := range lines {
		jobs <- struct {
			line  string
			index int
		}{line: line, index: i}
	}
	close(jobs)

	// Collect results in a separate goroutine
	go func() {
		for i := 0; i < len(lines); i++ {
			logLine := <-results
			mutex.Lock()
			logLines = append(logLines, logLine)
			mutex.Unlock()
		}
		close(results)
	}()

	// Wait for all workers to finish
	wg.Wait()

	// Sort log lines by timestamp (newest first)
	sort.Slice(logLines, func(i, j int) bool {
		// If timestamps are equal, preserve original order
		if logLines[i].Timestamp.Equal(logLines[j].Timestamp) {
			return logLines[i].Index < logLines[j].Index
		}
		return logLines[i].Timestamp.After(logLines[j].Timestamp)
	})

	// Find the closest log line to the target timestamp
	closestIndex := 0
	smallestDiff := time.Duration(1<<63 - 1) // Max duration
	for i, logLine := range logLines {
		if !logLine.Timestamp.IsZero() { // Skip lines without timestamps
			diff := absDuration(logLine.Timestamp.Sub(targetTimestamp))
			if diff < smallestDiff {
				smallestDiff = diff
				closestIndex = i
			}
		}
	}

	// Calculate the range of lines to include
	startIndex := closestIndex - lineCount
	if startIndex < 0 {
		startIndex = 0
	}
	endIndex := closestIndex + lineCount
	if endIndex >= len(logLines) {
		endIndex = len(logLines) - 1
	}

	// Handle the case where there's a large time jump
	// If there's a gap larger than 30 minutes, don't include lines from across the gap
	if hasLargeTimeJumps(logLines, startIndex, endIndex) {
		// Recompute range with a more restrictive approach
		startIndex = findNearbyIndex(logLines, closestIndex, lineCount, true)
		endIndex = findNearbyIndex(logLines, closestIndex, lineCount, false)
	}

	// Create a slice of filtered log lines in original time order (oldest first)
	filteredLines := make([]LogLine, 0, endIndex-startIndex+1)
	for i := startIndex; i <= endIndex; i++ {
		// Skip lines with zero timestamps (unparseable)
		if !logLines[i].Timestamp.IsZero() {
			filteredLines = append(filteredLines, logLines[i])
		}
	}

	// Create the output file
	outFile, err := os.Create(outputFile)
	if err != nil {
		return 0, errors.Wrap(ctx, err, "create filtered output file")
	}
	defer outFile.Close()

	writer := bufio.NewWriter(outFile)

	// Define additional header information specific to FilterByTimestamp
	additionalHeaderInfo := func(w *bufio.Writer) error {
		fmt.Fprintf(w, "# Showing %d lines before and after\n", lineCount)

		// Add warnings about time gaps
		if hasLargeTimeJumps(logLines, startIndex, endIndex) {
			fmt.Fprintf(w, "# WARNING: Large time gaps detected in the logs.\n")
			fmt.Fprintf(w, "# This may indicate gaps in the log coverage, possibly due to archive boundaries.\n")
		}
		return nil
	}

	// Use the helper function to write logs
	if err := writeFilteredLogs(
		ctx,
		writer,
		filteredLines,
		logLines,
		closestIndex,
		targetTimestampStr,
		smallestDiff,
		appName,
		additionalHeaderInfo,
	); err != nil {
		return 0, errors.Wrap(ctx, err, "write filtered logs")
	}

	return len(filteredLines), nil
}

// FilterByHours filters log lines within a certain number of hours before and after a specific timestamp
func FilterByHours(ctx context.Context, inputFile, outputFile, targetTimestampStr string, hoursCount int, appName string, statusCh chan<- status.StatusMessage) (int, error) {
	// Parse target timestamp
	targetTimestamp, err := timestamp.ParseSearch(ctx, targetTimestampStr)
	if err != nil {
		return 0, errors.Wrap(ctx, err, "parse target timestamp")
	}

	// Normalize target timestamp to UTC for consistent comparison
	targetTimestamp = targetTimestamp.UTC()

	// Calculate time range
	startTime := targetTimestamp.Add(-time.Duration(hoursCount) * time.Hour)
	endTime := targetTimestamp.Add(time.Duration(hoursCount) * time.Hour)

	// Open input file
	file, err := os.Open(inputFile)
	if err != nil {
		return 0, errors.Wrap(ctx, err, "open input file for filtering")
	}
	defer file.Close()

	// Read all lines into memory
	var lines []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		// Skip empty lines
		if line == "" {
			continue
		}
		lines = append(lines, line)
	}

	if err := scanner.Err(); err != nil {
		return 0, errors.Wrap(ctx, err, "read input file for filtering")
	}

	// Process in parallel using worker pool
	numWorkers := runtime.NumCPU()
	var logLines []LogLine
	var mutex sync.Mutex

	// Create channels for work distribution
	jobs := make(chan struct {
		line  string
		index int
	}, len(lines))
	results := make(chan LogLine, len(lines))

	// Create worker pool
	var wg sync.WaitGroup
	wg.Add(numWorkers)

	// Launch workers
	for w := 0; w < numWorkers; w++ {
		go func() {
			defer wg.Done()
			for job := range jobs {
				// Try to parse timestamp from the line
				ts, _ := timestamp.Parse(ctx, job.line)

				// Normalize timestamp to UTC for consistent comparison
				if !ts.IsZero() {
					ts = ts.UTC()
				}

				results <- LogLine{
					Timestamp: ts,
					Content:   job.line,
					Index:     job.index,
				}
			}
		}()
	}

	// Send jobs to workers
	for i, line := range lines {
		jobs <- struct {
			line  string
			index int
		}{line: line, index: i}
	}
	close(jobs)

	// Collect results in a separate goroutine
	go func() {
		for i := 0; i < len(lines); i++ {
			logLine := <-results
			mutex.Lock()
			logLines = append(logLines, logLine)
			mutex.Unlock()
		}
		close(results)
	}()

	// Wait for all workers to finish
	wg.Wait()

	// Filter logs that fall within the time range
	var filteredLines []LogLine
	closestIndex := -1
	smallestDiff := time.Duration(1<<63 - 1) // Max duration

	for i, logLine := range logLines {
		if !logLine.Timestamp.IsZero() { // Skip lines without timestamps
			// Calculate difference from target time for finding closest
			diff := absDuration(logLine.Timestamp.Sub(targetTimestamp))
			if diff < smallestDiff {
				smallestDiff = diff
				closestIndex = i
			}

			// If log is within time range, include it
			if (logLine.Timestamp.Equal(startTime) || logLine.Timestamp.After(startTime)) &&
				(logLine.Timestamp.Equal(endTime) || logLine.Timestamp.Before(endTime)) {
				filteredLines = append(filteredLines, logLine)
			}
		}
	}

	// Create the output file
	outFile, err := os.Create(outputFile)
	if err != nil {
		return 0, errors.Wrap(ctx, err, "create filtered output file")
	}
	defer outFile.Close()

	writer := bufio.NewWriter(outFile)

	// Try to load Europe/Paris location for display
	parisLoc, err := time.LoadLocation("Europe/Paris")
	if err != nil {
		parisLoc = time.Local
	}

	// Define additional header information specific to FilterByHours
	additionalHeaderInfo := func(w *bufio.Writer) error {
		// Convert time range to Europe/Paris for display
		displayStartTime := startTime.In(parisLoc)
		displayEndTime := endTime.In(parisLoc)

		fmt.Fprintf(w, "# Time range: %s to %s (Europe/Paris)\n",
			displayStartTime.Format("2006-01-02 15:04:05"),
			displayEndTime.Format("2006-01-02 15:04:05"))
		fmt.Fprintf(w, "# Hours before and after: %d\n", hoursCount)

		// Check for logs before and after target time
		var hasLogsBeforeTarget, hasLogsAfterTarget bool
		for _, logLine := range filteredLines {
			if logLine.Timestamp.Before(targetTimestamp) {
				hasLogsBeforeTarget = true
			}
			if logLine.Timestamp.After(targetTimestamp) {
				hasLogsAfterTarget = true
			}
		}

		if !hasLogsBeforeTarget {
			fmt.Fprintf(w, "# WARNING: No logs found before the target timestamp. This may indicate missing data.\n")
		}

		if !hasLogsAfterTarget {
			fmt.Fprintf(w, "# WARNING: No logs found after the target timestamp. This may indicate missing data.\n")
		}

		return nil
	}

	// Use the helper function to write logs
	if err := writeFilteredLogs(
		ctx,
		writer,
		filteredLines,
		logLines,
		closestIndex,
		targetTimestampStr,
		smallestDiff,
		appName,
		additionalHeaderInfo,
	); err != nil {
		return 0, errors.Wrap(ctx, err, "write filtered logs")
	}

	return len(filteredLines), nil
}

// absDuration returns the absolute value of a time.Duration
func absDuration(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

// hasLargeTimeJumps checks if there are gaps larger than 30 minutes in the log timeline
func hasLargeTimeJumps(logLines []LogLine, startIndex, endIndex int) bool {
	const maxTimeGap = 30 * time.Minute

	// Can't have large jumps if we don't have enough lines
	if endIndex-startIndex < 3 {
		return false
	}

	// Get lines with valid timestamps
	var validLines []LogLine
	for i := startIndex; i <= endIndex; i++ {
		if !logLines[i].Timestamp.IsZero() {
			validLines = append(validLines, logLines[i])
		}
	}

	// Check for large time jumps
	if len(validLines) < 3 {
		return false
	}

	// Since logs are sorted newest first, check for gaps
	for i := 0; i < len(validLines)-1; i++ {
		timeDiff := validLines[i].Timestamp.Sub(validLines[i+1].Timestamp)
		if timeDiff > maxTimeGap {
			return true
		}
	}

	return false
}

// findNearbyIndex finds a closer index based on time proximity to avoid large time jumps
func findNearbyIndex(logLines []LogLine, closestIndex, lineCount int, lookBefore bool) int {
	const maxTimeGap = 30 * time.Minute

	if closestIndex < 0 || closestIndex >= len(logLines) {
		return closestIndex
	}

	baseTime := logLines[closestIndex].Timestamp
	if baseTime.IsZero() {
		// If base line has no timestamp, use original calculation
		if lookBefore {
			return max(0, closestIndex-lineCount)
		}
		return min(len(logLines)-1, closestIndex+lineCount)
	}

	// Count how many valid lines we've found
	count := 0
	index := closestIndex

	// Direction depends on whether we're looking before or after
	direction := -1
	if !lookBefore {
		direction = 1
	}

	for count < lineCount && index >= 0 && index < len(logLines) {
		// Only count lines with valid timestamps within reasonable time range
		if !logLines[index].Timestamp.IsZero() {
			timeDiff := absDuration(logLines[index].Timestamp.Sub(baseTime))
			if timeDiff > maxTimeGap {
				// Stop if we hit a large time gap
				break
			}
			count++
		}

		// Move to next line in appropriate direction
		if count < lineCount {
			index += direction
		}
	}

	// Return the found index, adjusting for edge cases
	if lookBefore {
		// For looking before, we need the lower bound
		return max(0, index+1)
	}
	// For looking after, we need the upper bound
	return min(len(logLines)-1, index)
}

// max returns the larger of two integers
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// min returns the smaller of two integers
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
