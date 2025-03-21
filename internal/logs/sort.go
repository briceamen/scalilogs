package logs

import (
	"bufio"
	"context"
	"os"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Scalingo/go-utils/errors/v2"
	"github.com/briceamen/scalilogs/internal/timestamp"
)

// SortByTimestamp sorts the log lines in the input file by their timestamps
func SortByTimestamp(ctx context.Context, inputFile, outputFile string) (int, error) {
	// Open input file
	file, err := os.Open(inputFile)
	if err != nil {
		return 0, errors.Wrap(ctx, err, "open input file for sorting")
	}
	defer file.Close()

	// Read all lines into memory first (faster than using scanner in parallel)
	var lines []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		// Skip empty lines
		if strings.TrimSpace(line) == "" {
			continue
		}
		lines = append(lines, line)
	}

	if err := scanner.Err(); err != nil {
		return 0, errors.Wrap(ctx, err, "read input file for sorting")
	}

	// Count lines
	lineCount := len(lines)

	// Process in parallel using worker pool
	numWorkers := runtime.NumCPU() // Use all available CPU cores
	var logLines []LogLine
	var mutex sync.Mutex // To protect concurrent access to logLines

	// Create a channel to distribute work
	jobs := make(chan string, lineCount)
	results := make(chan LogLine, lineCount)

	// Launch workers
	var wg sync.WaitGroup
	wg.Add(numWorkers)

	// Launch workers
	for w := 0; w < numWorkers; w++ {
		go func() {
			defer wg.Done()
			for line := range jobs {
				// Try to parse timestamp from the line
				ts, err := timestamp.Parse(ctx, line)
				if err != nil {
					// Debug log the error but continue with a zero timestamp
					// For troubleshooting, you can uncomment the next line
					// fmt.Printf("Failed to parse timestamp from line: %s\nError: %v\n", line, err)
					ts = time.Time{}
				}

				// Normalize timestamp to UTC to ensure consistent sorting
				if !ts.IsZero() {
					ts = ts.UTC()
				}

				results <- LogLine{
					Timestamp: ts,
					Content:   line,
					Index:     0, // We don't need to track index for sorting
				}
			}
		}()
	}

	// Send jobs to workers
	for _, line := range lines {
		jobs <- line
	}
	close(jobs) // No more jobs to send

	// Collect results in a separate goroutine
	go func() {
		for i := 0; i < lineCount; i++ {
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
		// Handle case where one or both timestamps are zero
		if logLines[i].Timestamp.IsZero() && !logLines[j].Timestamp.IsZero() {
			return false // Zero timestamp sorts after non-zero
		}
		if !logLines[i].Timestamp.IsZero() && logLines[j].Timestamp.IsZero() {
			return true // Non-zero timestamp sorts before zero
		}

		// If timestamps are equal, preserve original order
		if logLines[i].Timestamp.Equal(logLines[j].Timestamp) {
			return i < j
		}
		return logLines[i].Timestamp.After(logLines[j].Timestamp)
	})

	// Write sorted lines to output file
	outFile, err := os.Create(outputFile)
	if err != nil {
		return 0, errors.Wrap(ctx, err, "create sorted output file")
	}
	defer outFile.Close()

	writer := bufio.NewWriter(outFile)
	for _, logLine := range logLines {
		if _, err := writer.WriteString(logLine.Content + "\n"); err != nil {
			return 0, errors.Wrap(ctx, err, "write sorted log line")
		}
	}

	if err := writer.Flush(); err != nil {
		return 0, errors.Wrap(ctx, err, "flush sorted output file")
	}

	return lineCount, nil
}
