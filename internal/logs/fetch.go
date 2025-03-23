package logs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/Scalingo/go-utils/errors/v2"
	"github.com/briceamen/scalilogs/internal/status"
	"github.com/briceamen/scalilogs/pkg/scalingo"
)

// fetchRecentLogs fetches the most recent logs for the app
func fetchRecentLogs(ctx context.Context, client *scalingo.Client, appName string, outputFile string) (int, error) {
	// Create a temporary file to write to, so we can count lines afterward
	tempFile := outputFile + ".tmp"
	file, err := os.Create(tempFile)
	if err != nil {
		return 0, errors.Wrap(ctx, err, "create temp output file")
	}
	defer func() {
		file.Close()
		os.Remove(tempFile) // Clean up temp file
	}()

	// Use a reasonable default line count instead of always requesting maximum
	// Scalingo API allows a maximum of 1,000,000 lines
	lineCount := 1000000

	// Fetch logs using the Scalingo client
	err = scalingo.FetchLogs(ctx, client.Scalingo, appName, lineCount, file)
	if err != nil {
		return 0, errors.Wrap(ctx, err, "fetch recent logs")
	}

	// Close the file so we can count lines
	file.Close()

	// Count lines in the file
	actualLineCount, err := countLines(tempFile)
	if err != nil {
		return 0, errors.Wrap(ctx, err, "count lines in recent logs")
	}

	// Copy the temp file to the original output file
	if err := copyFile(tempFile, outputFile); err != nil {
		return 0, errors.Wrap(ctx, err, "copy temp file to output file")
	}

	return actualLineCount, nil
}

// fetchSelectedArchives downloads and processes the selected archives in parallel
func fetchSelectedArchives(ctx context.Context, client *scalingo.Client, appName, outputFile string,
	archives []Archive, statusCh chan<- status.Message) (int, map[string]int, error) {

	totalLines := 0
	archiveDetails := make(map[string]int)

	// Create a temporary directory for archives
	tempDir := filepath.Dir(outputFile)

	// Create the output file
	outFile, err := os.Create(outputFile)
	if err != nil {
		return 0, nil, errors.Wrap(ctx, err, "create archive output file")
	}
	defer outFile.Close()

	// Process archives in parallel
	var wg sync.WaitGroup
	var mu sync.Mutex
	errCh := make(chan error, len(archives))

	// Determine the optimal number of workers
	maxWorkers := 4 // Set a reasonable limit to avoid overwhelming resources
	numWorkers := len(archives)
	if numWorkers > maxWorkers {
		numWorkers = maxWorkers
	}

	// Create a work queue and a channel to receive results
	type archiveJob struct {
		index   int
		archive Archive
	}

	type archiveResult struct {
		index          int
		lineCount      int
		archiveTimeKey string
		filePath       string
		err            error
	}

	jobs := make(chan archiveJob, len(archives))
	results := make(chan archiveResult, len(archives))

	// Start worker goroutines
	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			for job := range jobs {
				// Create a temporary file for this archive
				archiveFile := filepath.Join(tempDir, fmt.Sprintf("%s-archive-%d.log", appName, job.index))

				// Download the archive
				err := job.archive.DownloadToFile(ctx, client, archiveFile)
				if err != nil {
					results <- archiveResult{
						index: job.index,
						err:   errors.Wrap(ctx, err, "download archive"),
					}
					continue
				}

				// Count lines in this archive
				archiveLineCount, err := countLines(archiveFile)
				if err != nil {
					results <- archiveResult{
						index: job.index,
						err:   errors.Wrap(ctx, err, "count lines in archive"),
					}
					continue
				}

				// Create the time key
				archiveTimeKey := fmt.Sprintf("%s - %s",
					job.archive.FromTime.Format("2006-01-02 15:04"),
					job.archive.ToTime.Format("2006-01-02 15:04"))

				// Send result
				results <- archiveResult{
					index:          job.index,
					lineCount:      archiveLineCount,
					archiveTimeKey: archiveTimeKey,
					filePath:       archiveFile,
					err:            nil,
				}
			}
		}()
	}

	// Queue up the work
	for i, archive := range archives {
		jobs <- archiveJob{
			index:   i,
			archive: archive,
		}
	}
	close(jobs)

	// Start a goroutine to collect results
	done := make(chan struct{})
	go func() {
		defer close(done)

		// Store the results in order
		resultFiles := make([]string, len(archives))
		resultDetails := make(map[int]string)
		resultCounts := make(map[int]int)

		for i := 0; i < len(archives); i++ {
			result := <-results

			if result.err != nil {
				errCh <- result.err
				return
			}

			mu.Lock()
			totalLines += result.lineCount
			archiveDetails[result.archiveTimeKey] = result.lineCount
			resultFiles[result.index] = result.filePath
			resultDetails[result.index] = result.archiveTimeKey
			resultCounts[result.index] = result.lineCount
			mu.Unlock()
		}

		// Now append files in order
		for i := 0; i < len(archives); i++ {
			filePath := resultFiles[i]
			if filePath == "" {
				continue // Skip if there was an error
			}

			if err := appendArchiveToOutput(ctx, filePath, outputFile); err != nil {
				errCh <- errors.Wrap(ctx, err, fmt.Sprintf("append archive %d to output", i))
				return
			}

			// Clean up the archive file
			os.Remove(filePath)

			mu.Lock()
			status.Update(statusCh, fmt.Sprintf("processed archive %d/%d: %s (%d lines)",
				i+1, len(archives), resultDetails[i], resultCounts[i]))
			mu.Unlock()
		}
	}()

	// Wait for all workers to finish
	wg.Wait()
	<-done

	// Check if there were any errors
	select {
	case err := <-errCh:
		return totalLines, archiveDetails, err
	default:
		// No errors
	}

	return totalLines, archiveDetails, nil
}
