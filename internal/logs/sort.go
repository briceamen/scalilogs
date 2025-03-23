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
)

// logEntry represents a log line with its parsed timestamp
type logEntry struct {
	Line      string
	Timestamp time.Time
	Index     int // Original position to maintain ordering for equal timestamps
}

// Define a fixed-size chunk of log entries for efficient sorting
type logChunk struct {
	entries []logEntry
}

// SortByTimestamp sorts log entries in the input file by their timestamps
// and writes the sorted entries to the output file
func SortByTimestamp(ctx context.Context, inputFile, outputFile string) (int, error) {
	// Open input file
	input, err := os.Open(inputFile)
	if err != nil {
		return 0, errors.Wrap(ctx, err, "open input file")
	}
	defer input.Close()

	// Get file size for chunking
	fileInfo, err := input.Stat()
	if err != nil {
		return 0, errors.Wrap(ctx, err, "get file stats")
	}
	fileSize := fileInfo.Size()

	// Read all log entries using parallel processing
	const maxWorkers = 8
	numWorkers := runtime.NumCPU()
	if numWorkers > maxWorkers {
		numWorkers = maxWorkers
	}

	// Increase scanner buffer size for very long lines
	const maxCapacity = 10 * 1024 * 1024 // 10MB

	// Parallel processing of large files
	if fileSize > 10*1024*1024 { // Only parallelize for files > 10MB
		return optimizedParallelSort(ctx, inputFile, outputFile, numWorkers, maxCapacity)
	}

	// For smaller files, use the original approach
	scanner := bufio.NewScanner(input)
	var entries []logEntry

	buf := make([]byte, maxCapacity)
	scanner.Buffer(buf, maxCapacity)

	index := 0
	for scanner.Scan() {
		line := scanner.Text()

		// Parse timestamp from the line
		ts, _ := timestamp.Parse(ctx, line)

		// Add the entry with its timestamp (zero value if parsing failed)
		entries = append(entries, logEntry{
			Line:      line,
			Timestamp: ts,
			Index:     index,
		})
		index++
	}

	if err := scanner.Err(); err != nil {
		return 0, errors.Wrap(ctx, err, "read input file")
	}

	// Sort entries by timestamp
	sort.Slice(entries, func(i, j int) bool {
		return compareLogEntries(entries[i], entries[j])
	})

	// Create output file
	output, err := os.Create(outputFile)
	if err != nil {
		return 0, errors.Wrap(ctx, err, "create output file")
	}
	defer output.Close()

	// Write sorted entries to output file
	writer := bufio.NewWriter(output)
	for _, entry := range entries {
		if _, err := fmt.Fprintln(writer, entry.Line); err != nil {
			return 0, errors.Wrap(ctx, err, "write to output file")
		}
	}

	if err := writer.Flush(); err != nil {
		return 0, errors.Wrap(ctx, err, "flush output file")
	}

	return len(entries), nil
}

// compareLogEntries compares two log entries for sorting
func compareLogEntries(a, b logEntry) bool {
	// For entries without valid timestamps, preserve original order
	if a.Timestamp.IsZero() && b.Timestamp.IsZero() {
		return a.Index < b.Index
	}

	// Entries with timestamps come before those without
	if a.Timestamp.IsZero() {
		return false
	}
	if b.Timestamp.IsZero() {
		return true
	}

	// If timestamps are equal, preserve original order
	if a.Timestamp.Equal(b.Timestamp) {
		return a.Index < b.Index
	}

	// Sort by timestamp (oldest first)
	return a.Timestamp.Before(b.Timestamp)
}

// optimizedParallelSort sorts log entries in the input file by their timestamps
// and writes the sorted entries to the output file
func optimizedParallelSort(ctx context.Context, inputFile, outputFile string, numWorkers int, maxCapacity int) (int, error) {
	// Step 1: Read and partition the file into chunks for parallel processing
	chunks, totalLines, err := partitionFile(ctx, inputFile, numWorkers, maxCapacity)
	if err != nil {
		return 0, errors.Wrap(ctx, err, "partition input file")
	}

	if totalLines == 0 {
		return 0, nil
	}

	// Step 2: Sort each chunk in parallel
	var wg sync.WaitGroup
	for i := range chunks {
		wg.Add(1)
		go func(chunkIndex int) {
			defer wg.Done()
			// Sort this chunk
			sort.Slice(chunks[chunkIndex].entries, func(i, j int) bool {
				// For entries without valid timestamps, preserve original order
				if chunks[chunkIndex].entries[i].Timestamp.IsZero() && chunks[chunkIndex].entries[j].Timestamp.IsZero() {
					return chunks[chunkIndex].entries[i].Index < chunks[chunkIndex].entries[j].Index
				}

				// Entries with timestamps come before those without
				if chunks[chunkIndex].entries[i].Timestamp.IsZero() {
					return false
				}
				if chunks[chunkIndex].entries[j].Timestamp.IsZero() {
					return true
				}

				// If timestamps are equal, preserve original order
				if chunks[chunkIndex].entries[i].Timestamp.Equal(chunks[chunkIndex].entries[j].Timestamp) {
					return chunks[chunkIndex].entries[i].Index < chunks[chunkIndex].entries[j].Index
				}

				// Sort by timestamp (oldest first)
				return chunks[chunkIndex].entries[i].Timestamp.Before(chunks[chunkIndex].entries[j].Timestamp)
			})
		}(i)
	}

	// Wait for all sorting to complete
	wg.Wait()

	// Step 3: Merge the sorted chunks using a priority queue approach
	allEntries, err := mergeChunks(chunks, totalLines)
	if err != nil {
		return 0, errors.Wrap(ctx, err, "merge sorted chunks")
	}

	// Step 4: Write the merged results to the output file
	err = writeResults(ctx, outputFile, allEntries)
	if err != nil {
		return 0, errors.Wrap(ctx, err, "write merged results")
	}

	return totalLines, nil
}

// partitionFile divides the input file into balanced chunks for parallel processing
func partitionFile(ctx context.Context, inputFile string, numWorkers int, maxCapacity int) ([]logChunk, int, error) {
	// Open input file
	input, err := os.Open(inputFile)
	if err != nil {
		return nil, 0, errors.Wrap(ctx, err, "open input file")
	}
	defer input.Close()

	// First, count the lines to determine chunk size
	lineCount, err := countLines(inputFile)
	if err != nil {
		return nil, 0, errors.Wrap(ctx, err, "count lines in input file")
	}

	if lineCount == 0 {
		return nil, 0, nil
	}

	// Calculate optimal chunk size based on line count and worker count
	// Ensure each worker gets a reasonable amount of work
	chunkSize := (lineCount + numWorkers - 1) / numWorkers
	if chunkSize < 5000 {
		// For small files, use fewer workers with larger chunks
		chunkSize = 5000
		numWorkers = (lineCount + chunkSize - 1) / chunkSize
	}

	// Create scanner with large buffer for long lines
	scanner := bufio.NewScanner(input)
	buf := make([]byte, maxCapacity)
	scanner.Buffer(buf, maxCapacity)

	// Allocate chunks
	chunks := make([]logChunk, numWorkers)
	totalLinesRead := 0

	// Distribute lines to chunks
	for i := 0; i < numWorkers; i++ {
		chunkLines := chunkSize
		if i == numWorkers-1 {
			// Last chunk gets remaining lines
			chunkLines = lineCount - (i * chunkSize)
		}

		// Pre-allocate the entries slice for this chunk
		chunks[i].entries = make([]logEntry, 0, chunkLines)

		// Read lines for this chunk
		for j := 0; j < chunkLines && scanner.Scan(); j++ {
			line := scanner.Text()

			// Parse timestamp from the line
			ts, _ := timestamp.Parse(ctx, line)

			// Add the entry with its timestamp
			chunks[i].entries = append(chunks[i].entries, logEntry{
				Line:      line,
				Timestamp: ts,
				Index:     totalLinesRead,
			})
			totalLinesRead++
		}

		// Check for errors
		if err := scanner.Err(); err != nil {
			return nil, 0, errors.Wrap(ctx, err, "read input file lines")
		}
	}

	return chunks, totalLinesRead, nil
}

// mergeChunks efficiently merges multiple sorted chunks
func mergeChunks(chunks []logChunk, totalLines int) ([]logEntry, error) {
	// For a small number of chunks, use a simple priority queue approach
	if len(chunks) <= 4 {
		return mergeSmallNumberOfChunks(chunks, totalLines)
	}

	// For larger numbers of chunks, use a hierarchical merge
	return mergeHierarchical(chunks, totalLines)
}

// mergeSmallNumberOfChunks merges up to 4 chunks using a direct approach
func mergeSmallNumberOfChunks(chunks []logChunk, totalLines int) ([]logEntry, error) {
	// Initialize result array
	result := make([]logEntry, 0, totalLines)

	// Set up indices for each chunk
	indices := make([]int, len(chunks))

	// Keep merging until all chunks are processed
	for {
		// Find the chunk with the earliest timestamp
		nextChunk := -1
		var earliestTime time.Time

		for i := 0; i < len(chunks); i++ {
			// Skip if this chunk is fully processed
			if indices[i] >= len(chunks[i].entries) {
				continue
			}

			// Get current entry from this chunk
			current := chunks[i].entries[indices[i]]

			// Special case: entries without timestamps go last,
			// but maintain original order among themselves
			if current.Timestamp.IsZero() {
				if nextChunk == -1 || (!chunks[nextChunk].entries[indices[nextChunk]].Timestamp.IsZero() ||
					current.Index < chunks[nextChunk].entries[indices[nextChunk]].Index) {
					nextChunk = i
				}
				continue
			}

			// First valid timestamp or earlier than current earliest
			if nextChunk == -1 ||
				chunks[nextChunk].entries[indices[nextChunk]].Timestamp.IsZero() ||
				current.Timestamp.Before(earliestTime) ||
				(current.Timestamp.Equal(earliestTime) && current.Index < chunks[nextChunk].entries[indices[nextChunk]].Index) {
				nextChunk = i
				earliestTime = current.Timestamp
			}
		}

		// If no valid chunk found, we're done
		if nextChunk == -1 {
			break
		}

		// Add the next entry to results
		result = append(result, chunks[nextChunk].entries[indices[nextChunk]])
		indices[nextChunk]++
	}

	return result, nil
}

// mergeHierarchical implements a divide-and-conquer approach for merging many chunks
func mergeHierarchical(chunks []logChunk, totalLines int) ([]logEntry, error) {
	// Base case: just one chunk
	if len(chunks) == 1 {
		return chunks[0].entries, nil
	}

	// Divide chunks into two groups and merge recursively
	mid := len(chunks) / 2
	var wg sync.WaitGroup
	wg.Add(2)

	var leftResult, rightResult []logEntry
	var leftErr, rightErr error

	// Process left half
	go func() {
		defer wg.Done()
		leftResult, leftErr = mergeHierarchical(chunks[:mid], totalLines/2)
	}()

	// Process right half
	go func() {
		defer wg.Done()
		rightResult, rightErr = mergeHierarchical(chunks[mid:], totalLines/2)
	}()

	// Wait for both halves to complete
	wg.Wait()

	// Check for errors
	if leftErr != nil {
		return nil, leftErr
	}
	if rightErr != nil {
		return nil, rightErr
	}

	// Merge the two sorted halves
	result := make([]logEntry, 0, len(leftResult)+len(rightResult))
	l, r := 0, 0

	for l < len(leftResult) && r < len(rightResult) {
		// Handle the zero timestamp case
		if leftResult[l].Timestamp.IsZero() && rightResult[r].Timestamp.IsZero() {
			// Both have zero timestamps, use original index
			if leftResult[l].Index < rightResult[r].Index {
				result = append(result, leftResult[l])
				l++
			} else {
				result = append(result, rightResult[r])
				r++
			}
			continue
		}

		if leftResult[l].Timestamp.IsZero() {
			// Left entry has no timestamp, right has timestamp
			result = append(result, rightResult[r])
			r++
			continue
		}

		if rightResult[r].Timestamp.IsZero() {
			// Right entry has no timestamp, left has timestamp
			result = append(result, leftResult[l])
			l++
			continue
		}

		// Compare timestamps
		if leftResult[l].Timestamp.Equal(rightResult[r].Timestamp) {
			// Equal timestamps, use original index
			if leftResult[l].Index < rightResult[r].Index {
				result = append(result, leftResult[l])
				l++
			} else {
				result = append(result, rightResult[r])
				r++
			}
		} else if leftResult[l].Timestamp.Before(rightResult[r].Timestamp) {
			// Left timestamp is earlier
			result = append(result, leftResult[l])
			l++
		} else {
			// Right timestamp is earlier
			result = append(result, rightResult[r])
			r++
		}
	}

	// Append remaining entries
	result = append(result, leftResult[l:]...)
	result = append(result, rightResult[r:]...)

	return result, nil
}

// writeResults writes the sorted entries to the output file
func writeResults(ctx context.Context, outputFile string, entries []logEntry) error {
	// Create output file
	output, err := os.Create(outputFile)
	if err != nil {
		return errors.Wrap(ctx, err, "create output file")
	}
	defer output.Close()

	// Use buffered writer for efficiency
	writer := bufio.NewWriter(output)
	for _, entry := range entries {
		if _, err := fmt.Fprintln(writer, entry.Line); err != nil {
			return errors.Wrap(ctx, err, "write to output file")
		}
	}

	// Flush the buffer to ensure all data is written
	if err := writer.Flush(); err != nil {
		return errors.Wrap(ctx, err, "flush output file")
	}

	return nil
}
