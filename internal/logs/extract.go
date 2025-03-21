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
	"github.com/briceamen/scalilogs/internal/archive"
	"github.com/briceamen/scalilogs/internal/status"
	"github.com/briceamen/scalilogs/internal/timestamp"
	"github.com/briceamen/scalilogs/internal/tui"
	"github.com/briceamen/scalilogs/pkg/scalingo"
)

// ExtractLogs extracts logs for the specified app around a target timestamp
func ExtractLogs(ctx context.Context, client *scalingo.ScalingoClient, appName, targetTimestamp string, lineCount int, hoursCount int) (string, error) {
	var outputFilePath string

	extractFunc := func(statusCh chan status.StatusMessage, errorCh chan status.ErrorMessage, finishCh chan status.FinishMessage) error {
		// Record start time for performance tracking
		startTime := time.Now()

		// Update status
		status.UpdateStatus(statusCh, "initializing log extraction")

		// Recreate the client with statusCh to capture token exchange and region selection logs
		recreatedClient, err := scalingo.NewScalingoClient(ctx, client.Env, client.Region, statusCh)
		if err != nil {
			return status.ReportError(ctx, errorCh, err)
		}
		client = recreatedClient

		// Create timestamped output file
		ts := time.Now().Format("20060102-150405")
		outputDir := filepath.Join(os.TempDir(), "scalingo-logs")
		if err := os.MkdirAll(outputDir, 0755); err != nil {
			return errors.Wrap(ctx, err, "create output directory")
		}

		// Temporary file for unsorted logs
		tempOutputFile := filepath.Join(outputDir, fmt.Sprintf("%s-%s-unsorted.log", appName, ts))

		// Final sorted output file
		outputFile := filepath.Join(outputDir, fmt.Sprintf("%s-%s.log", appName, ts))

		// Parse target timestamp if provided (for archive filtering)
		var targetTime time.Time

		if targetTimestamp != "" {
			targetTime, err = timestamp.ParseSearch(ctx, targetTimestamp)
			if err != nil {
				return errors.Wrap(ctx, err, "parse target timestamp")
			}
		}

		// Update status
		status.UpdateStatus(statusCh, "checking available log archives")

		// Determine if we need archived logs based on the target timestamp
		// We always fetch recent logs for complete coverage
		needsArchivedLogs := false

		if !targetTime.IsZero() {
			// First, fetch available archives to check their timestamps
			archivesResp, err := client.FetchLogsArchives(ctx, appName)
			if err != nil {
				// If we can't fetch archives, default to recent logs only
				status.UpdateStatus(statusCh, "warning: failed to fetch archives list, using recent logs only")
			} else if len(archivesResp.Archives) > 0 {
				// Get the most recent archive end time
				latestArchive := archivesResp.Archives[0]
				// Parse the "To" timestamp of the most recent archive
				latestArchiveEnd, err := time.Parse("Mon Jan 2 15:04:05 -0700 MST 2006", latestArchive.To)
				if err != nil {
					status.UpdateStatus(statusCh, "warning: failed to parse archive end time, using both recent and archived logs")
					needsArchivedLogs = true
				} else {
					// Calculate time range based on hours if set, otherwise use a default buffer
					timeBuffer := 2 * time.Hour
					if hoursCount > 0 {
						timeBuffer = time.Duration(hoursCount) * time.Hour
					} else if lineCount > 0 {
						// Estimate time range from line count (assuming ~1 line per second with buffer)
						estimatedSeconds := lineCount * 3 / 2
						timeBuffer = time.Duration(estimatedSeconds) * time.Second

						// Ensure a reasonable minimum time buffer
						if timeBuffer < 5*time.Minute {
							timeBuffer = 5 * time.Minute
						}
					}

					// Calculate the range start time based on the target time and buffer
					rangeStartTime := targetTime.Add(-timeBuffer)

					// Always include archives if:
					// 1. Target time is before or equal to the latest archive end time, OR
					// 2. The time range starts before the latest archive end time, OR
					// 3. Target time is within timeBuffer of the latest archive end time
					// This handles the edge case where logs span both live logs and archives
					if !targetTime.After(latestArchiveEnd) ||
						!rangeStartTime.After(latestArchiveEnd) ||
						targetTime.Sub(latestArchiveEnd) < timeBuffer {
						statusMsg := fmt.Sprintf("logs may span archives and live logs: target time %s, latest archive ends at %s",
							targetTime.Format("2006-01-02 15:04:05"),
							latestArchiveEnd.Format("2006-01-02 15:04:05"))
						status.UpdateStatus(statusCh, statusMsg)
						needsArchivedLogs = true
					} else {
						statusMsg := fmt.Sprintf("target time %s is after archives (latest ends at %s)",
							targetTime.Format("2006-01-02 15:04:05"),
							latestArchiveEnd.Format("2006-01-02 15:04:05"))
						status.UpdateStatus(statusCh, statusMsg)
					}
				}
			}
		}

		// Track log counts from different sources
		liveLogsCount := 0
		archiveLogsCount := 0
		archiveDetails := make(map[string]int)

		// Always get recent logs for complete coverage
		status.UpdateStatus(statusCh, "fetching recent logs")
		liveLogsCount, err = fetchRecentLogs(ctx, client.Client, appName, tempOutputFile)
		if err != nil {
			return status.ReportError(ctx, errorCh, err)
		}
		status.UpdateStatus(statusCh, "fetched recent logs", liveLogsCount)

		// Get archived logs if needed
		if needsArchivedLogs {
			status.UpdateStatus(statusCh, "fetching archived logs")
			archiveLogsCount, archiveDetails, err = archive.FetchArchived(ctx, client.Client, appName, outputDir, tempOutputFile, targetTime, statusCh, hoursCount, lineCount)
			if err != nil {
				return status.ReportError(ctx, errorCh, err)
			}
			status.UpdateStatus(statusCh, "fetched archive logs", archiveLogsCount)
		}

		// Sort the logs by timestamp
		status.UpdateStatus(statusCh, "sorting logs by timestamp")
		totalLines, err := SortByTimestamp(ctx, tempOutputFile, outputFile)
		if err != nil {
			return status.ReportError(ctx, errorCh, err)
		}
		status.UpdateStatus(statusCh, "sorted total lines", totalLines)

		// If targeting a specific timestamp, filter logs
		filteredLineCount := 0
		if targetTimestamp != "" {
			if hoursCount > 0 {
				// Filter by hours around the timestamp
				statusMsg := fmt.Sprintf("filtering logs around timestamp: %s (±%d hours)", targetTimestamp, hoursCount)
				status.UpdateStatus(statusCh, statusMsg)

				filterOutputFile := filepath.Join(outputDir, fmt.Sprintf("%s-%s-filtered.log", appName, ts))
				filteredLineCount, err = FilterByHours(ctx, outputFile, filterOutputFile, targetTimestamp, hoursCount, appName, statusCh)
				if err != nil {
					return status.ReportError(ctx, errorCh, err)
				}
				outputFile = filterOutputFile
				status.UpdateStatus(statusCh, "filtered logs", filteredLineCount)
			} else if lineCount > 0 {
				// Filter by lines around the timestamp
				statusMsg := fmt.Sprintf("filtering logs around timestamp: %s (±%d lines)", targetTimestamp, lineCount)
				status.UpdateStatus(statusCh, statusMsg)

				filterOutputFile := filepath.Join(outputDir, fmt.Sprintf("%s-%s-filtered.log", appName, ts))
				filteredLineCount, err = FilterByTimestamp(ctx, outputFile, filterOutputFile, targetTimestamp, lineCount, appName, statusCh)
				if err != nil {
					return status.ReportError(ctx, errorCh, err)
				}
				outputFile = filterOutputFile
				status.UpdateStatus(statusCh, "filtered logs", filteredLineCount)
			}
		}

		// Clean up temporary files
		os.Remove(tempOutputFile)

		// Send finish message with all details
		elapsedTime := time.Since(startTime)
		tui.FinishProcess(finishCh, outputFile, liveLogsCount, archiveLogsCount, totalLines, filteredLineCount, archiveDetails, elapsedTime)

		outputFilePath = outputFile
		return nil
	}

	if err := tui.RunExtractor(ctx, appName, targetTimestamp, extractFunc); err != nil {
		return "", err
	}

	return outputFilePath, nil
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

	// Copy the contents
	_, err = io.Copy(destFile, sourceFile)
	if err != nil {
		return fmt.Errorf("copy file contents: %w", err)
	}

	return nil
}
