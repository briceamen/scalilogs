package logs

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/Scalingo/go-utils/errors/v2"
	"github.com/briceamen/scalilogs/internal/status"
	"github.com/briceamen/scalilogs/internal/timestamp"
)

// min returns the smaller of x or y
func min(x, y int) int {
	if x < y {
		return x
	}
	return y
}

// timestampLine represents a log line with parsed timestamp
type timestampLine struct {
	line      string
	timestamp time.Time
	valid     bool
}

// FilterByTimestamp filters logs from the input file to show a specific number
// of lines before and after the target timestamp, centering the results around
// the target timestamp
func FilterByTimestamp(ctx context.Context, inputFile, outputFile, targetTimestampStr string, lineCount int, appName string, statusCh chan<- status.Message) (int, error) {
	// Parse the target timestamp
	targetTime, err := timestamp.ParseSearch(ctx, targetTimestampStr)
	if err != nil {
		return 0, errors.Wrap(ctx, err, "parse target timestamp")
	}

	// Process the input file with optimized strategy
	result, err := optimizedFilterByTimestamp(ctx, inputFile, outputFile, targetTime, lineCount, statusCh)
	if err != nil {
		return 0, errors.Wrap(ctx, err, "optimized timestamp filtering")
	}

	return result, nil
}

// optimizedFilterByTimestamp filters logs from the input file to show a specific number
// of lines before and after the target timestamp, centering the results around
// the target timestamp
func optimizedFilterByTimestamp(ctx context.Context, inputFile, outputFile string,
	targetTime time.Time, lineCount int, statusCh chan<- status.Message) (int, error) {

	// Open input file for reading
	input, err := os.Open(inputFile)
	if err != nil {
		return 0, errors.Wrap(ctx, err, "open input file")
	}
	defer input.Close()

	// Create a memory-efficient scanner
	reader := bufio.NewReader(input)

	// Channels for parallel processing
	lineChan := make(chan string, 1000)
	resultChan := make(chan timestampLine, 1000)
	errChan := make(chan error, 1)
	doneChan := make(chan struct{})
	const numWorkers = 8

	// Start timestamp parsing workers
	var wg sync.WaitGroup
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			for line := range lineChan {
				ts, err := timestamp.Parse(ctx, line)
				if err != nil {
					// Line without valid timestamp
					resultChan <- timestampLine{line: line, valid: false}
					continue
				}

				// Line with valid timestamp
				resultChan <- timestampLine{
					line:      line,
					timestamp: ts,
					valid:     true,
				}
			}
		}()
	}

	// Start a goroutine to close resultChan when all workers are done
	go func() {
		wg.Wait()
		close(resultChan)
	}()

	// Pre-allocate slices with reasonable capacity
	const initialCapacity = 10000 // Start with space for 10K lines
	allLines := make([]string, 0, initialCapacity)
	validIndices := make([]int, 0, initialCapacity)

	// Variables for tracking closest match
	var (
		closestDiff  time.Duration = -1
		closestIndex int           = -1
	)

	// Start goroutine to read file and send lines to workers
	go func() {
		defer close(lineChan)
		defer close(doneChan)

		lineIndex := 0
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				if err == io.EOF {
					break
				}
				errChan <- errors.Wrap(ctx, err, "read input file")
				return
			}
			lineChan <- line
			lineIndex++
		}
	}()

	// Convert target time to UTC for consistent comparison
	targetTimeUTC := targetTime.UTC()

	// Process results
	lineIndex := 0
	for result := range resultChan {
		// Store each line
		allLines = append(allLines, result.line)

		if result.valid {
			// Track this valid timestamp
			validIndices = append(validIndices, lineIndex)

			// Check if this timestamp is closer to the target
			// Convert to UTC for consistent comparison
			resultTimeUTC := result.timestamp.UTC()

			diff := resultTimeUTC.Sub(targetTimeUTC)
			if diff < 0 {
				diff = -diff // Get absolute value
			}

			if closestDiff == -1 || diff < closestDiff {
				closestDiff = diff
				closestIndex = lineIndex
			}
		}
		lineIndex++
	}

	// Check for errors from file reading
	select {
	case err := <-errChan:
		return 0, err
	case <-doneChan:
		// Normal completion
	default:
	}

	// If we didn't find any valid timestamp, return an error
	if closestIndex == -1 {
		return 0, errors.New(ctx, "no log line with valid timestamp found")
	}

	status.Update(statusCh, fmt.Sprintf("found closest log line at position %d", closestIndex+1))

	// Find the position of the closest timestamp in our validIndices slice
	closestPos := -1
	for i, idx := range validIndices {
		if idx == closestIndex {
			closestPos = i
			break
		}
	}

	if closestPos == -1 {
		// This shouldn't happen as we already found the closest index
		return 0, errors.New(ctx, "internal error: closest log line not found in valid indices")
	}

	// Calculate the total number of entries we want to include
	total := 1 + (lineCount * 2) // 1 for the closest entry itself + lineCount before and after

	var selectedIndices []int

	// Efficiently determine which log lines to include
	if len(validIndices) <= total {
		// If there are fewer valid entries than we need, take all of them
		selectedIndices = validIndices
	} else {
		// Initialize with closest position
		start := closestPos
		end := closestPos + 1
		remaining := total - 1 // already have 1 (the closest match)

		// Expand outward from the closest position, alternating before/after
		// until we've included enough entries or exhausted one direction
		for remaining > 0 {
			// Try to add one entry before
			if start > 0 && remaining > 0 {
				start--
				remaining--
			}

			// Try to add one entry after
			if end < len(validIndices) && remaining > 0 {
				end++
				remaining--
			}

			// If we can't expand in either direction, we're done
			if start == 0 && end == len(validIndices) {
				break
			}

			// If one direction is exhausted, use all remaining from other direction
			if start == 0 && remaining > 0 {
				// Use remaining slots for entries after
				needed := min(remaining, len(validIndices)-end)
				end += needed
				remaining -= needed
				break
			}

			if end == len(validIndices) && remaining > 0 {
				// Use remaining slots for entries before
				needed := min(remaining, start)
				start -= needed
				remaining -= needed
				break
			}
		}

		// Get the indices from the range we've calculated
		selectedIndices = validIndices[start:end]
	}

	// Sort the indices to maintain original file order
	sort.Ints(selectedIndices)

	// Create output file
	output, err := os.Create(outputFile)
	if err != nil {
		return 0, errors.Wrap(ctx, err, "create output file")
	}
	defer output.Close()

	writer := bufio.NewWriter(output)

	// Write the filtered lines to the output file
	for _, i := range selectedIndices {
		line := allLines[i]

		// Mark the target line with >>>
		if i == closestIndex {
			line = ">>> " + line
		}

		if _, err := writer.WriteString(line); err != nil {
			return 0, errors.Wrap(ctx, err, "write to output file")
		}
	}

	if err := writer.Flush(); err != nil {
		return 0, errors.Wrap(ctx, err, "flush output file")
	}

	return len(selectedIndices), nil
}

// FilterByHours filters logs from the input file to show logs within a specific
// number of hours before and after the target timestamp and properly centers
// the target timestamp in the results
func FilterByHours(ctx context.Context, inputFile, outputFile, targetTimestampStr string, hoursCount int, appName string, statusCh chan<- status.Message) (int, error) {
	// Parse the target timestamp
	targetTime, err := timestamp.ParseSearch(ctx, targetTimestampStr)
	if err != nil {
		return 0, errors.Wrap(ctx, err, "parse target timestamp")
	}

	// Process the input file with optimized strategy
	result, err := optimizedFilterByHours(ctx, inputFile, outputFile, targetTime, hoursCount, statusCh)
	if err != nil {
		return 0, errors.Wrap(ctx, err, "optimized hours filtering")
	}

	return result, nil
}

// optimizedFilterByHours implements a more efficient filtering algorithm for hours-based filtering
func optimizedFilterByHours(ctx context.Context, inputFile, outputFile string,
	targetTime time.Time, hoursCount int, statusCh chan<- status.Message) (int, error) {

	// Handle the time filtering with timezone awareness
	// First, preserve the original local timezone for display
	startTime := targetTime.Add(-time.Duration(hoursCount) * time.Hour)
	endTime := targetTime.Add(time.Duration(hoursCount) * time.Hour)

	// Now convert to UTC for consistent comparisons
	targetTimeUTC := targetTime.UTC()
	startTimeUTC := startTime.UTC()
	endTimeUTC := endTime.UTC()

	status.Update(statusCh, fmt.Sprintf("filtering logs from %s to %s",
		startTime.Format("2006-01-02 15:04:05"),
		endTime.Format("2006-01-02 15:04:05")))

	// Open input file for reading
	input, err := os.Open(inputFile)
	if err != nil {
		return 0, errors.Wrap(ctx, err, "open input file")
	}
	defer input.Close()

	// Create output file for writing
	output, err := os.Create(outputFile)
	if err != nil {
		return 0, errors.Wrap(ctx, err, "create output file")
	}
	defer output.Close()

	reader := bufio.NewReader(input)
	writer := bufio.NewWriter(output)

	// Channel for parallel processing
	lineChan := make(chan string, 1000)
	resultChan := make(chan timestampLine, 1000)
	errChan := make(chan error, 1)
	doneChan := make(chan struct{})
	const numWorkers = 8

	// Start timestamp parsing workers
	var wg sync.WaitGroup
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			for line := range lineChan {
				ts, err := timestamp.Parse(ctx, line)
				if err != nil {
					// Line without valid timestamp
					resultChan <- timestampLine{line: line, valid: false}
					continue
				}

				// Line with valid timestamp
				resultChan <- timestampLine{
					line:      line,
					timestamp: ts,
					valid:     true,
				}
			}
		}()
	}

	// Start a goroutine to close resultChan when all workers are done
	go func() {
		wg.Wait()
		close(resultChan)
	}()

	// Start goroutine to read file and send lines to workers
	go func() {
		defer close(lineChan)
		defer close(doneChan)

		lineIndex := 0
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				if err == io.EOF {
					break
				}
				errChan <- errors.Wrap(ctx, err, "read input file")
				return
			}
			lineChan <- line
			lineIndex++
		}
	}()

	// Pre-allocate slices with reasonable capacity
	const initialCapacity = 10000 // Start with space for 10K lines
	var (
		allLines                      = make([]string, 0, initialCapacity)
		filteredIndices               = make([]int, 0, initialCapacity)
		closestDiff     time.Duration = -1
		closestIdx      int           = -1
	)

	// Process results
	lineIndex := 0
	for result := range resultChan {
		// Store each line
		allLines = append(allLines, result.line)

		if result.valid {
			// Store valid timestamps

			// Convert to UTC for consistent comparison
			resultTimeUTC := result.timestamp.UTC()

			// Check if timestamp is in range (using UTC)
			if (resultTimeUTC.Equal(startTimeUTC) || resultTimeUTC.After(startTimeUTC)) &&
				(resultTimeUTC.Equal(endTimeUTC) || resultTimeUTC.Before(endTimeUTC)) {
				filteredIndices = append(filteredIndices, lineIndex)
			}

			// Track the closest entry to target time (using UTC)
			diff := resultTimeUTC.Sub(targetTimeUTC)
			if diff < 0 {
				diff = -diff // Get absolute value
			}

			if closestDiff == -1 || diff < closestDiff {
				closestDiff = diff
				closestIdx = lineIndex
			}
		}
		lineIndex++
	}

	// Check for errors from file reading
	select {
	case err := <-errChan:
		return 0, err
	case <-doneChan:
		// Normal completion
	default:
	}

	// If we didn't find any valid timestamp, return an error
	if closestIdx == -1 {
		return 0, errors.New(ctx, "no log line with valid timestamp found")
	}

	status.Update(statusCh, "found closest log line to target timestamp")

	// Sort filtered indices to maintain original order
	sort.Ints(filteredIndices)

	// Verify target timestamp is included in filtered results
	targetIncluded := false
	for _, idx := range filteredIndices {
		if idx == closestIdx {
			targetIncluded = true
			break
		}
	}

	// If target not included, make sure to include it
	if !targetIncluded && closestIdx != -1 {
		filteredIndices = append(filteredIndices, closestIdx)
		sort.Ints(filteredIndices)
	}

	// Write the filtered lines to the output file
	lineCount := 0
	for _, idx := range filteredIndices {
		line := allLines[idx]

		// Mark the target line with >>>
		if idx == closestIdx {
			line = ">>> " + line
		}

		if _, err := writer.WriteString(line); err != nil {
			return 0, errors.Wrap(ctx, err, "write to output file")
		}
		lineCount++
	}

	if err := writer.Flush(); err != nil {
		return 0, errors.Wrap(ctx, err, "flush output file")
	}

	return lineCount, nil
}
