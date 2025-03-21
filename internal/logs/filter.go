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
	"github.com/briceamen/scalilogs/internal/timestamp"
	"github.com/briceamen/scalilogs/internal/tui"
)

// LogLine represents a single log line with its timestamp
type LogLine struct {
	Timestamp time.Time
	Content   string
	Index     int // Store original line index for preserving ordering
}

// FilterByTimestamp filters log lines around a specific timestamp, keeping a certain number of lines before and after
func FilterByTimestamp(ctx context.Context, inputFile, outputFile, targetTimestampStr string, lineCount int, appName string, statusCh chan<- tui.StatusMessage) (int, error) {
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

	// Report the closest timestamp found
	if closestIndex >= 0 && !logLines[closestIndex].Timestamp.IsZero() {
		closestTime := logLines[closestIndex].Timestamp.Format("2006-01-02 15:04:05")
		statusMsg := fmt.Sprintf("found closest timestamp at %s (difference: %s)",
			closestTime, smallestDiff)
		tui.UpdateStatus(statusCh, statusMsg)

		extractedMsg := fmt.Sprintf("extracted %d lines around the target time",
			len(filteredLines))
		tui.UpdateStatus(statusCh, extractedMsg)
	}

	// Sort by original index to maintain log sequence
	sort.Slice(filteredLines, func(i, j int) bool {
		return filteredLines[i].Index < filteredLines[j].Index
	})

	// Write filtered lines to output file
	outFile, err := os.Create(outputFile)
	if err != nil {
		return 0, errors.Wrap(ctx, err, "create filtered output file")
	}
	defer outFile.Close()

	writer := bufio.NewWriter(outFile)
	for _, logLine := range filteredLines {
		if _, err := writer.WriteString(logLine.Content + "\n"); err != nil {
			return 0, errors.Wrap(ctx, err, "write filtered log line")
		}
	}

	if err := writer.Flush(); err != nil {
		return 0, errors.Wrap(ctx, err, "flush filtered output file")
	}

	return len(filteredLines), nil
}

// FilterByHours filters log lines within a certain number of hours before and after a specific timestamp
func FilterByHours(ctx context.Context, inputFile, outputFile, targetTimestampStr string, hoursCount int, appName string, statusCh chan<- tui.StatusMessage) (int, error) {
	// Parse target timestamp
	targetTimestamp, err := timestamp.ParseSearch(ctx, targetTimestampStr)
	if err != nil {
		return 0, errors.Wrap(ctx, err, "parse target timestamp")
	}

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

	// Report the closest timestamp found
	if closestIndex >= 0 && !logLines[closestIndex].Timestamp.IsZero() {
		closestTime := logLines[closestIndex].Timestamp.Format("2006-01-02 15:04:05")
		statusMsg := fmt.Sprintf("found closest timestamp at %s (difference: %s)",
			closestTime, smallestDiff)
		tui.UpdateStatus(statusCh, statusMsg)

		extractedMsg := fmt.Sprintf("extracted %d logs within ±%d hours of the target time",
			len(filteredLines), hoursCount)
		tui.UpdateStatus(statusCh, extractedMsg)
	}

	// Sort by original index to maintain log sequence
	sort.Slice(filteredLines, func(i, j int) bool {
		return filteredLines[i].Index < filteredLines[j].Index
	})

	// Write filtered lines to output file
	outFile, err := os.Create(outputFile)
	if err != nil {
		return 0, errors.Wrap(ctx, err, "create filtered output file")
	}
	defer outFile.Close()

	writer := bufio.NewWriter(outFile)
	for _, logLine := range filteredLines {
		if _, err := writer.WriteString(logLine.Content + "\n"); err != nil {
			return 0, errors.Wrap(ctx, err, "write filtered log line")
		}
	}

	if err := writer.Flush(); err != nil {
		return 0, errors.Wrap(ctx, err, "flush filtered output file")
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
