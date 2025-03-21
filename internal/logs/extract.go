package logs

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/Scalingo/go-utils/errors/v2"
	"github.com/briceamen/logaround/internal/archive"
	"github.com/briceamen/logaround/internal/timestamp"
	"github.com/briceamen/logaround/internal/ui"
	"github.com/briceamen/logaround/pkg/scalingo"
)

// ExtractLogs extracts logs for the specified app around a target timestamp
func ExtractLogs(ctx context.Context, client *scalingo.ScalingoClient, appName, targetTimestamp string, lineCount int, hoursCount int) (string, error) {
	// Record start time for performance tracking
	startTime := time.Now()

	// Create timestamped output file
	ts := time.Now().Format("20060102-150405")
	outputDir := filepath.Join(os.TempDir(), "scalingo-logs")
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return "", errors.Wrap(ctx, err, "create output directory")
	}

	// Temporary file for unsorted logs
	tempOutputFile := filepath.Join(outputDir, fmt.Sprintf("%s-%s-unsorted.log", appName, ts))

	// Final sorted output file
	outputFile := filepath.Join(outputDir, fmt.Sprintf("%s-%s.log", appName, ts))

	// Parse target timestamp if provided (for archive filtering)
	var targetTime time.Time
	var err error
	if targetTimestamp != "" {
		targetTime, err = timestamp.ParseSearch(ctx, targetTimestamp)
		if err != nil {
			return "", errors.Wrap(ctx, err, "parse target timestamp")
		}
	}

	// Start spinner
	spinner := ui.NewSpinner("Checking available log archives...")
	spinner.Start()
	defer spinner.Stop()

	// Determine if we need archived logs based on the target timestamp
	// We always fetch recent logs for complete coverage
	needsArchivedLogs := false

	if !targetTime.IsZero() {
		// First, fetch available archives to check their timestamps
		fmt.Println("Checking available log archives...")
		archivesResp, err := client.FetchLogsArchives(ctx, appName)
		if err != nil {
			// If we can't fetch archives, default to recent logs only
			fmt.Printf("Warning: Failed to fetch archives list: %v. Will use recent logs only.\n", err)
		} else if len(archivesResp.Archives) > 0 {
			// Get the most recent archive end time
			latestArchive := archivesResp.Archives[0]
			// Parse the "To" timestamp of the most recent archive
			latestArchiveEnd, err := time.Parse("Mon Jan 2 15:04:05 -0700 MST 2006", latestArchive.To)
			if err != nil {
				fmt.Printf("Warning: Failed to parse archive end time: %v. Will use both recent and archived logs.\n", err)
				needsArchivedLogs = true
			} else {
				// If target time is older than or close to the latest archive, include archived logs
				spinner.Stop() // Stop spinner to print message clearly
				if !targetTime.After(latestArchiveEnd) || targetTime.Sub(latestArchiveEnd) < 2*time.Hour {
					fmt.Printf("Target time %s is within or close to archives (latest ends at %s)\n\n",
						targetTime.Format("2006-01-02 15:04:05"),
						latestArchiveEnd.Format("2006-01-02 15:04:05"))
					needsArchivedLogs = true
				} else {
					fmt.Printf("Target time %s is after the latest archive (ends at %s)\n\n",
						targetTime.Format("2006-01-02 15:04:05"),
						latestArchiveEnd.Format("2006-01-02 15:04:05"))
				}
				spinner.Start() // Restart spinner for next operation
			}
		}
	}

	// Track log counts from different sources
	liveLogsCount := 0
	archiveLogsCount := 0
	archiveDetails := make(map[string]int)

	// Always get recent logs for complete coverage
	spinner.Stop()
	fmt.Println("Fetching recent logs for " + appName + "...")
	spinner.Start()
	spinner.Update("Fetching recent logs...")
	liveLogsCount, err = fetchRecentLogs(ctx, client.Client, appName, tempOutputFile)
	if err != nil {
		return "", err
	}

	// Get archived logs if needed
	if needsArchivedLogs {
		spinner.Stop()
		fmt.Println("Fetching archived logs for " + appName + "...")
		spinner.Start()
		spinner.Update("Fetching archived logs...")
		archiveLogsCount, archiveDetails, err = archive.FetchArchived(ctx, client.Client, appName, outputDir, tempOutputFile, targetTime, spinner)
		if err != nil {
			return "", err
		}
	}

	// Sort the logs by timestamp
	spinner.Stop()
	fmt.Println("Sorting logs by timestamp...")
	spinner.Start()
	spinner.Update("Sorting logs by timestamp...")
	totalLines, err := SortByTimestamp(ctx, tempOutputFile, outputFile)
	if err != nil {
		return "", err
	}

	// If targeting a specific timestamp, filter logs
	filteredLineCount := 0
	if targetTimestamp != "" {
		spinner.Stop()
		if hoursCount > 0 {
			// Filter by hours around the timestamp
			fmt.Printf("Filtering logs around timestamp: %s (±%d hours)...\n", targetTimestamp, hoursCount)
			spinner.Start()
			spinner.Update("Filtering logs around target timestamp by hours...")
			filterOutputFile := filepath.Join(outputDir, fmt.Sprintf("%s-%s-filtered.log", appName, ts))
			filteredLineCount, err = FilterByHours(ctx, outputFile, filterOutputFile, targetTimestamp, hoursCount, appName)
			if err != nil {
				return "", err
			}
			outputFile = filterOutputFile
		} else if lineCount > 0 {
			// Filter by lines around the timestamp
			fmt.Printf("Filtering logs around timestamp: %s (±%d lines)...\n", targetTimestamp, lineCount)
			spinner.Start()
			spinner.Update("Filtering logs around target timestamp by lines...")
			filterOutputFile := filepath.Join(outputDir, fmt.Sprintf("%s-%s-filtered.log", appName, ts))
			filteredLineCount, err = FilterByTimestamp(ctx, outputFile, filterOutputFile, targetTimestamp, lineCount, appName)
			if err != nil {
				return "", err
			}
			outputFile = filterOutputFile
		}
	}

	// Stop spinner before final summary
	spinner.Stop()

	// Display summary of logs fetched
	elapsedTime := time.Since(startTime)
	fmt.Println("\n--- Logs Fetch Summary ---")
	fmt.Printf("- Live logs: %d lines\n", liveLogsCount)
	fmt.Printf("- Archive logs: %d lines\n", archiveLogsCount)
	if len(archiveDetails) > 0 {
		fmt.Println("  Archives:")
		for archiveTime, count := range archiveDetails {
			fmt.Printf("  - %s: %d lines\n", archiveTime, count)
		}
	}
	fmt.Printf("- Total combined logs: %d lines\n", totalLines)
	if filteredLineCount > 0 {
		fmt.Printf("- Final filtered logs: %d lines\n", filteredLineCount)
	}
	fmt.Printf("- Total processing time: %s\n", elapsedTime.Round(time.Millisecond))
	fmt.Println("------------------------")

	// Clean up temporary files
	os.Remove(tempOutputFile)

	return outputFile, nil
}

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

	// Always use maximum number of lines to ensure full coverage
	// This helps prevent gaps in log coverage between archives and recent logs
	maxLineCount := 1000000

	// Fetch logs using the Scalingo client
	err = scalingo.FetchLogs(ctx, client, appName, maxLineCount, file)
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

	fmt.Printf("Fetched %d lines of recent logs\n", actualLineCount)
	return actualLineCount, nil
}

// countLines counts the number of lines in a file
func countLines(filename string) (int, error) {
	file, err := os.Open(filename)
	if err != nil {
		return 0, err
	}
	defer file.Close()

	count := 0
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		count++
	}

	if err := scanner.Err(); err != nil {
		return count, err
	}

	return count, nil
}

// copyFile copies a file from src to dst
func copyFile(src, dst string) error {
	// Open source file
	sourceFile, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open source file: %w", err)
	}
	defer sourceFile.Close()

	// Create destination file
	destFile, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("create destination file: %w", err)
	}
	defer destFile.Close()

	// Copy content
	if _, err := io.Copy(destFile, sourceFile); err != nil {
		return fmt.Errorf("copy file content: %w", err)
	}

	return nil
}
