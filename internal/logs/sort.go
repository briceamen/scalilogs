package logs

import (
	"bufio"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/briceamen/logaround/internal/timestamp"
)

// SortByTimestamp sorts the log lines in the input file by their timestamps
func SortByTimestamp(inputFile, outputFile string) error {
	fmt.Println("Sorting logs by timestamp...")

	// Open input file
	file, err := os.Open(inputFile)
	if err != nil {
		return fmt.Errorf("open input file for sorting: %w", err)
	}
	defer file.Close()

	// Read all lines and parse timestamps
	var logLines []LogLine
	scanner := bufio.NewScanner(file)

	for scanner.Scan() {
		line := scanner.Text()

		// Skip empty lines
		if strings.TrimSpace(line) == "" {
			continue
		}

		// Try to parse timestamp from the line
		ts, err := timestamp.Parse(line)
		if err != nil {
			// If we can't parse timestamp, add with zero time (will be at the beginning)
			logLines = append(logLines, LogLine{
				Timestamp: ts,
				Content:   line,
			})
			continue
		}

		logLines = append(logLines, LogLine{
			Timestamp: ts,
			Content:   line,
		})
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read input file for sorting: %w", err)
	}

	// Sort log lines by timestamp (newest first)
	sort.Slice(logLines, func(i, j int) bool {
		return logLines[i].Timestamp.After(logLines[j].Timestamp)
	})

	// Write sorted lines to output file
	outFile, err := os.Create(outputFile)
	if err != nil {
		return fmt.Errorf("create sorted output file: %w", err)
	}
	defer outFile.Close()

	writer := bufio.NewWriter(outFile)
	for _, logLine := range logLines {
		if _, err := writer.WriteString(logLine.Content + "\n"); err != nil {
			return fmt.Errorf("write sorted log line: %w", err)
		}
	}

	if err := writer.Flush(); err != nil {
		return fmt.Errorf("flush sorted output file: %w", err)
	}

	fmt.Printf("Sorted %d log lines\n", len(logLines))
	return nil
}
