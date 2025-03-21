package logs

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"runtime"
	"sort"
	"strings"
	"sync"

	"github.com/Scalingo/go-utils/errors/v2"
	"github.com/briceamen/logaround/internal/timestamp"
)

// SortByTimestamp sorts the log lines in the input file by their timestamps
func SortByTimestamp(ctx context.Context, inputFile, outputFile string) (int, error) {
	fmt.Println("Sorting logs by timestamp...")

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

	// Create worker pool
	var wg sync.WaitGroup
	wg.Add(numWorkers)

	// Launch workers
	for w := 0; w < numWorkers; w++ {
		go func() {
			defer wg.Done()
			for line := range jobs {
				// Try to parse timestamp from the line
				ts, err := timestamp.Parse(ctx, line)
				results <- LogLine{
					Timestamp: ts,
					Content:   line,
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

	fmt.Printf("Sorted %d log lines\n\n", lineCount)
	return lineCount, nil
}
