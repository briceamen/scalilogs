package logs

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Scalingo/go-utils/errors/v2"
	"github.com/briceamen/scalilogs/internal/archive"
	"github.com/briceamen/scalilogs/internal/status"
	"github.com/briceamen/scalilogs/internal/timestamp"
	"github.com/briceamen/scalilogs/pkg/scalingo"
)

// SearchResult represents the result of a log search operation
type SearchResult struct {
	OutputFile       string
	LiveLogsCount    int
	ArchiveLogsCount int
	TotalLines       int
	FilteredLines    int
	ArchiveDetails   map[string]int
	ElapsedTime      time.Duration
	// Detailed timing information
	ArchiveSelectionTime time.Duration
	FetchLiveTime        time.Duration
	FetchArchiveTime     time.Duration
	SortTime             time.Duration
	FilterTime           time.Duration
}

// LogSearchConfig holds configuration for log search operations
type LogSearchConfig struct {
	AppName         string
	TargetTimestamp string
	LineCount       int
	HoursCount      int
	StreamResults   bool
	OutputDir       string
}

// SearchLogs is the main entry point for the reworked log search algorithm
func SearchLogs(ctx context.Context, client *scalingo.Client, config LogSearchConfig, statusCh chan<- status.Message) (*SearchResult, error) {
	// Record start time for performance tracking
	startTime := time.Now()
	result := &SearchResult{
		ArchiveDetails: make(map[string]int),
	}

	// Parse target timestamp if provided
	var targetTime time.Time
	var err error

	if config.TargetTimestamp != "" {
		targetTime, err = timestamp.ParseSearch(ctx, config.TargetTimestamp)
		if err != nil {
			return nil, errors.New(ctx, "invalid timestamp format")
		}
	}

	// Create output directory
	if config.OutputDir == "" {
		config.OutputDir = filepath.Join(os.TempDir(), "scalingo-logs")
	}

	if err := os.MkdirAll(config.OutputDir, 0755); err != nil {
		return nil, errors.New(ctx, "create temporary output directory")
	}

	// Create timestamped output file
	ts := time.Now().Format("20060102-150405")
	tempOutputFile := filepath.Join(config.OutputDir, fmt.Sprintf("%s-%s-unsorted.log", config.AppName, ts))
	mainOutputFile := filepath.Join(config.OutputDir, fmt.Sprintf("%s-%s.log", config.AppName, ts))

	status.Update(statusCh, "analyzing logs timeline")
	analysisStartTime := time.Now()

	archivesToFetch, err := determineRequiredArchives(ctx, client, config.AppName, targetTime, config.HoursCount, config.LineCount, statusCh)
	if err != nil {
		// If the error is about historical archives not being available, create a clean, simple error message
		if strings.Contains(err.Error(), "no archives found containing or overlapping with the requested date") {
			// Extract date range from the error message
			errMsg := err.Error()
			dateRange := ""
			if idx := strings.Index(errMsg, "Available archives only cover"); idx != -1 {
				dateRange = errMsg[idx+len("Available archives only cover"):]
			}

			// Create a new error rather than wrapping the existing one
			return nil, errors.New(ctx, fmt.Sprintf("No logs available for date %s. Available archives only cover%s",
				config.TargetTimestamp, dateRange))
		}
		// Pass through the original error without additional wrapping
		return nil, err
	}

	// Record analysis time
	result.ArchiveSelectionTime = time.Since(analysisStartTime)
	status.Update(statusCh, fmt.Sprintf("archive selection completed in %s", result.ArchiveSelectionTime.Round(time.Millisecond)))

	// Step 2: Determine if we need live logs
	needsLiveLogs := false

	// Check if we need live logs (no archives or target time is recent)
	if len(archivesToFetch) == 0 {
		needsLiveLogs = true
		status.Update(statusCh, "no relevant archives found, will use live logs")
	} else {
		// Get most recent archive time
		mostRecentArchive := archivesToFetch[len(archivesToFetch)-1]

		// If target time is more recent than the most recent archive end time
		if targetTime.IsZero() || targetTime.After(mostRecentArchive.ToTime) {
			needsLiveLogs = true
			status.Update(statusCh, "target time is more recent than available archives, will include live logs")
		}

		// If we're searching with a time range that extends past the most recent archive
		if config.HoursCount > 0 && !targetTime.IsZero() {
			rangeEndTime := targetTime.Add(time.Duration(config.HoursCount) * time.Hour)
			if rangeEndTime.After(mostRecentArchive.ToTime) {
				needsLiveLogs = true
				status.Update(statusCh, "time range extends beyond available archives, will include live logs")
			}
		}
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	errCh := make(chan error, 2)

	// Process archives if needed
	if len(archivesToFetch) > 0 {
		wg.Add(1)
		go func() {
			defer wg.Done()

			status.Update(statusCh, fmt.Sprintf("fetching %d archives", len(archivesToFetch)))
			archiveStartTime := time.Now()

			// Create a separate file for archive logs
			archiveFile := tempOutputFile + ".archives"

			count, details, err := fetchSelectedArchives(ctx, client, config.AppName, archiveFile, archivesToFetch, statusCh)
			if err != nil {
				errCh <- errors.New(ctx, "fetch archive logs")
				return
			}

			mu.Lock()
			result.ArchiveLogsCount = count
			result.ArchiveDetails = details
			result.FetchArchiveTime = time.Since(archiveStartTime)

			// Append the archive file to the main output file
			if err := appendArchiveToOutput(ctx, archiveFile, tempOutputFile); err != nil {
				errCh <- errors.New(ctx, "process archive files")
				mu.Unlock()
				return
			}

			// Clean up the archive file
			os.Remove(archiveFile)
			mu.Unlock()

			status.Update(statusCh, fmt.Sprintf("processed %d archive log lines in %s",
				count, result.FetchArchiveTime.Round(time.Millisecond)))
		}()
	}

	// Fetch live logs if needed
	if needsLiveLogs {
		wg.Add(1)
		go func() {
			defer wg.Done()

			status.Update(statusCh, "fetching live logs")
			liveStartTime := time.Now()

			// Create a separate file for live logs
			liveFile := tempOutputFile + ".live"

			count, err := fetchRecentLogs(ctx, client, config.AppName, liveFile)
			if err != nil {
				errCh <- errors.New(ctx, "fetch live logs")
				return
			}

			mu.Lock()
			result.LiveLogsCount = count
			result.FetchLiveTime = time.Since(liveStartTime)

			// Append the live logs file to the main output file
			if err := appendArchiveToOutput(ctx, liveFile, tempOutputFile); err != nil {
				errCh <- errors.New(ctx, "process live logs")
				mu.Unlock()
				return
			}

			// Clean up the live logs file
			os.Remove(liveFile)
			mu.Unlock()

			status.Update(statusCh, fmt.Sprintf("processed %d live log lines in %s",
				count, result.FetchLiveTime.Round(time.Millisecond)))
		}()
	}

	// Wait for all goroutines to finish
	wg.Wait()

	// Check if there were any errors
	select {
	case err := <-errCh:
		return nil, err
	default:
		// No errors
	}

	// If we didn't fetch any logs, inform the user
	if result.ArchiveLogsCount == 0 && result.LiveLogsCount == 0 {
		return nil, errors.Wrap(ctx, err, "no logs found matching the specified criteria")
	}

	status.Update(statusCh, "sorting logs by timestamp")
	sortStartTime := time.Now()

	totalLines, err := SortByTimestamp(ctx, tempOutputFile, mainOutputFile)
	if err != nil {
		return nil, errors.Wrap(ctx, err, "sort logs by timestamp")
	}

	result.TotalLines = totalLines
	result.SortTime = time.Since(sortStartTime)
	status.Update(statusCh, fmt.Sprintf("sorted %d total log lines in %s",
		totalLines, result.SortTime.Round(time.Millisecond)))

	// Step 5: Filter the logs by timestamp and range if specified
	filterStartTime := time.Now()
	if config.TargetTimestamp != "" {
		filterOutputFile := filepath.Join(config.OutputDir, fmt.Sprintf("%s-%s-filtered.log", config.AppName, ts))

		if config.HoursCount > 0 {
			// Filter by hours around the timestamp
			statusMsg := fmt.Sprintf("filtering logs around timestamp: %s (±%d hours)",
				config.TargetTimestamp, config.HoursCount)
			status.Update(statusCh, statusMsg)

			filteredLineCount, err := FilterByHours(ctx, mainOutputFile, filterOutputFile,
				config.TargetTimestamp, config.HoursCount, config.AppName, statusCh)
			if err != nil {
				return nil, errors.Wrap(ctx, err, "filter logs by time range")
			}

			result.FilteredLines = filteredLineCount
			result.OutputFile = filterOutputFile
			result.FilterTime = time.Since(filterStartTime)
			status.Update(statusCh, fmt.Sprintf("filtered to %d log lines in %s",
				filteredLineCount, result.FilterTime.Round(time.Millisecond)))
		} else if config.LineCount > 0 {
			// Filter by lines around the timestamp
			statusMsg := fmt.Sprintf("filtering logs around timestamp: %s (±%d lines)",
				config.TargetTimestamp, config.LineCount)
			status.Update(statusCh, statusMsg)

			filteredLineCount, err := FilterByTimestamp(ctx, mainOutputFile, filterOutputFile,
				config.TargetTimestamp, config.LineCount, config.AppName, statusCh)
			if err != nil {
				return nil, errors.Wrap(ctx, err, "filter logs around timestamp")
			}

			result.FilteredLines = filteredLineCount
			result.OutputFile = filterOutputFile
			result.FilterTime = time.Since(filterStartTime)
			status.Update(statusCh, fmt.Sprintf("filtered to %d log lines in %s",
				filteredLineCount, result.FilterTime.Round(time.Millisecond)))
		} else {
			// No filtering
			result.OutputFile = mainOutputFile
			result.FilterTime = time.Since(filterStartTime)
		}
	} else {
		// No filtering
		result.OutputFile = mainOutputFile
		result.FilterTime = time.Since(filterStartTime)
	}

	// Clean up temporary files
	os.Remove(tempOutputFile)

	// Calculate elapsed time
	result.ElapsedTime = time.Since(startTime)

	return result, nil
}

// Archive represents a log archive with its time range
type Archive struct {
	Item     scalingo.LogsArchiveItem
	FromTime time.Time
	ToTime   time.Time
	Index    int
}

// determineRequiredArchives figures out which archives to fetch based on the target timestamp
func determineRequiredArchives(ctx context.Context, client *scalingo.Client, appName string,
	targetTime time.Time, hoursCount, lineCount int, statusCh chan<- status.Message) ([]Archive, error) {

	// Get list of archives
	archivesResp, err := archive.FetchLogsArchives(ctx, statusCh, client, appName)
	if err != nil {
		// Pass through the error without additional wrapping
		return nil, err
	}

	// Check if there are no archives available
	if len(archivesResp.Archives) == 0 {
		status.Update(statusCh, "no log archives available")
		return nil, nil
	}

	// Process and parse the timestamps from the archives
	processedArchives := make([]Archive, 0, len(archivesResp.Archives))
	for i, archive := range archivesResp.Archives {
		// Parse timestamps from strings
		fromTime, err := time.Parse("Mon Jan 2 15:04:05 -0700 MST 2006", archive.From)
		if err != nil {
			return nil, errors.Wrap(ctx, err, "parse archive time format")
		}

		toTime, err := time.Parse("Mon Jan 2 15:04:05 -0700 MST 2006", archive.To)
		if err != nil {
			return nil, errors.Wrap(ctx, err, "parse archive time format")
		}

		processedArchives = append(processedArchives, Archive{
			Item:     archive,
			FromTime: fromTime,
			ToTime:   toTime,
			Index:    i,
		})
	}

	// Sort archives by time (newest first) for easier access to the most recent one
	sort.Slice(processedArchives, func(i, j int) bool {
		return processedArchives[i].ToTime.After(processedArchives[j].ToTime)
	})

	// If no target time specified, just return the most recent archive
	if targetTime.IsZero() {
		status.Update(statusCh, "no target time specified, using most recent archive")
		return processedArchives[:1], nil
	}
	
	// If target time is more recent than the latest archive, skip archives completely
	if !targetTime.IsZero() && targetTime.After(processedArchives[0].ToTime) {
		status.Update(statusCh, fmt.Sprintf("target time %s is more recent than newest archive (%s), skipping archives", 
			targetTime.Format("2006-01-02 15:04:05"), 
			processedArchives[0].ToTime.Format("2006-01-02 15:04:05")))
		return nil, nil
	}

	// For historical searching, optimize archive order
	isHistorical := targetTime.Before(time.Now().AddDate(-1, 0, 0))

	if isHistorical {
		// For historical searches, sort oldest first for more efficient searching
		sort.Slice(processedArchives, func(i, j int) bool {
			return processedArchives[i].FromTime.Before(processedArchives[j].FromTime)
		})
		status.Update(statusCh, fmt.Sprintf("searching for historical logs from %s, optimizing for older archives",
			targetTime.Format("2006-01-02 15:04:05")))
	} else {
		// For recent searches, sort newest first (default behavior)
		sort.Slice(processedArchives, func(i, j int) bool {
			return processedArchives[i].ToTime.After(processedArchives[j].ToTime)
		})
	}

	// Calculate time range based on parameters
	var startRangeTime, endRangeTime time.Time

	if hoursCount > 0 {
		// If hours specified, define range around target time
		startRangeTime = targetTime.Add(-time.Duration(hoursCount) * time.Hour)
		endRangeTime = targetTime.Add(time.Duration(hoursCount) * time.Hour)
		status.Update(statusCh, fmt.Sprintf("searching for logs between %s and %s",
			startRangeTime.Format("2006-01-02 15:04:05"),
			endRangeTime.Format("2006-01-02 15:04:05")))
	} else if lineCount > 0 {
		// If only line count specified, use a narrower time estimate
		// We'll expand this if needed, but start conservative
		estimatedTimePerLine := 2 * time.Second // Assuming ~30 lines per minute
		timeBuffer := time.Duration(lineCount) * estimatedTimePerLine

		// Add minimum buffer to ensure we get enough context
		if timeBuffer < 5*time.Minute {
			timeBuffer = 5 * time.Minute
		}

		startRangeTime = targetTime.Add(-timeBuffer)
		endRangeTime = targetTime.Add(timeBuffer)
		status.Update(statusCh, fmt.Sprintf("estimated time range for %d lines: %s",
			lineCount, formatDuration(2*timeBuffer)))
	} else {
		// Default to small window around target time if no specific parameters
		startRangeTime = targetTime.Add(-15 * time.Minute)
		endRangeTime = targetTime.Add(15 * time.Minute)
	}

	// Find archives that contain any part of our target time range
	var relevantArchives []Archive

	// First look for exact matches - archives containing target time
	for _, archive := range processedArchives {
		if (targetTime.Equal(archive.FromTime) || targetTime.After(archive.FromTime)) &&
			(targetTime.Equal(archive.ToTime) || targetTime.Before(archive.ToTime)) {
			relevantArchives = append(relevantArchives, archive)
			status.Update(statusCh, fmt.Sprintf("found archive containing target time: %s to %s",
				archive.FromTime.Format("2006-01-02 15:04:05"),
				archive.ToTime.Format("2006-01-02 15:04:05")))
		}
	}

	// Look for archives overlapping with our time range
	// Important: we always check for ALL archives that overlap with the range,
	// even if we found an exact match for the target time
	for _, archive := range processedArchives {
		// Skip if we already included this archive (from exact match)
		alreadyIncluded := false
		for _, included := range relevantArchives {
			if archive.Index == included.Index {
				alreadyIncluded = true
				break
			}
		}
		if alreadyIncluded {
			continue
		}

		// Include if archive overlaps with our search range
		if (archive.FromTime.Before(endRangeTime) || archive.FromTime.Equal(endRangeTime)) &&
			(archive.ToTime.After(startRangeTime) || archive.ToTime.Equal(startRangeTime)) {
			relevantArchives = append(relevantArchives, archive)
			status.Update(statusCh, fmt.Sprintf("found archive overlapping with search range: %s to %s",
				archive.FromTime.Format("2006-01-02 15:04:05"),
				archive.ToTime.Format("2006-01-02 15:04:05")))
		}
	}

	// If still no relevant archives AND we're looking for historical data,
	// we should return an error rather than using unrelated archives
	if len(relevantArchives) == 0 && isHistorical {
		// First check the available time range to give better feedback
		var oldestTime, newestTime time.Time

		if len(processedArchives) > 0 {
			oldestTime = processedArchives[0].FromTime
			newestTime = processedArchives[0].ToTime

			for _, archive := range processedArchives {
				if archive.FromTime.Before(oldestTime) {
					oldestTime = archive.FromTime
				}
				if archive.ToTime.After(newestTime) {
					newestTime = archive.ToTime
				}
			}

			rangeMsg := fmt.Sprintf("available archives only cover %s to %s",
				oldestTime.Format("2006-01-02"),
				newestTime.Format("2006-01-02"))
			status.Update(statusCh, rangeMsg)

			return nil, errors.New(ctx, fmt.Sprintf(
				"No archives found for date %s. Available archives only cover %s to %s",
				targetTime.Format("2006-01-02"),
				oldestTime.Format("2006-01-02"),
				newestTime.Format("2006-01-02")))
		}

		return nil, errors.New(ctx, fmt.Sprintf(
			"No archives found for date %s",
			targetTime.Format("2006-01-02")))
	}

	// For non-historical searches, if still no relevant archives, find closest ones
	if len(relevantArchives) == 0 {
		var closestBefore, closestAfter *Archive
		var minBeforeDiff, minAfterDiff time.Duration = -1, -1

		for i := range processedArchives {
			archive := &processedArchives[i]

			// Archive ends before target time
			if archive.ToTime.Before(targetTime) {
				diff := targetTime.Sub(archive.ToTime)
				if minBeforeDiff == -1 || diff < minBeforeDiff {
					minBeforeDiff = diff
					closestBefore = archive
				}
			}

			// Archive starts after target time
			if archive.FromTime.After(targetTime) {
				diff := archive.FromTime.Sub(targetTime)
				if minAfterDiff == -1 || diff < minAfterDiff {
					minAfterDiff = diff
					closestAfter = archive
				}
			}
		}

		// If we have archives both before and after
		if closestBefore != nil && closestAfter != nil {
			gap := closestAfter.FromTime.Sub(closestBefore.ToTime)

			// If gap is relatively small, include both
			if gap < 2*time.Hour {
				relevantArchives = append(relevantArchives, *closestBefore, *closestAfter)
				status.Update(statusCh, fmt.Sprintf("target time falls in %s gap between archives, including both",
					formatDuration(gap)))
			} else {
				// Large gap, just use the closer one
				if minBeforeDiff < minAfterDiff {
					relevantArchives = append(relevantArchives, *closestBefore)
					status.Update(statusCh, fmt.Sprintf("target time is %s after closest archive",
						formatDuration(minBeforeDiff)))
				} else {
					relevantArchives = append(relevantArchives, *closestAfter)
					status.Update(statusCh, fmt.Sprintf("target time is %s before closest archive",
						formatDuration(minAfterDiff)))
				}
			}
		} else if closestBefore != nil {
			relevantArchives = append(relevantArchives, *closestBefore)
			status.Update(statusCh, "target time is after all archives, using most recent")
		} else if closestAfter != nil {
			relevantArchives = append(relevantArchives, *closestAfter)
			status.Update(statusCh, "target time is before all archives, using oldest")
		}
	}

	// Sort relevant archives by start time (oldest first) to ensure chronological order
	sort.Slice(relevantArchives, func(i, j int) bool {
		return relevantArchives[i].FromTime.Before(relevantArchives[j].FromTime)
	})

	// Summary of archives to fetch
	if len(relevantArchives) > 0 {
		status.Update(statusCh, fmt.Sprintf("will fetch %d archives for requested time range",
			len(relevantArchives)))
	} else {
		status.Update(statusCh, "no relevant archives found for requested time range")
	}

	return relevantArchives, nil
}

// DownloadToFile downloads an archive to a file
func (a Archive) DownloadToFile(ctx context.Context, client *scalingo.Client, outputFile string) error {
	// Use the archive downloader with progress UI
	archiveFileName := fmt.Sprintf("archive-%d", a.Index)

	// Create a temporary file for the compressed data
	tempGzFile, err := os.CreateTemp("", fmt.Sprintf("logs-archive-%s-*.gz", archiveFileName))
	if err != nil {
		return errors.Wrap(ctx, err, "create temporary file for download")
	}
	tempGzPath := tempGzFile.Name()
	tempGzFile.Close()          // Close it so we can reopen it later
	defer os.Remove(tempGzPath) // Clean up temp file when done

	// Create the archive downloader
	downloader := archive.NewArchiveDownloader(ctx, client, a.Item.URL)

	// Download directly using the download function
	tempFile, err := os.Create(tempGzPath)
	if err != nil {
		return errors.Wrap(ctx, err, "create temp file for download")
	}
	defer tempFile.Close()

	err = downloader.DownloadToWriter(ctx, tempFile)
	if err != nil {
		return errors.Wrap(ctx, err, "download archive")
	}

	// Create the output file
	outputFileHandle, err := os.Create(outputFile)
	if err != nil {
		return errors.Wrap(ctx, err, "create output file")
	}
	defer outputFileHandle.Close()

	// Use zcat to decompress the gzip file
	cmd := exec.Command("zcat", tempGzPath)
	cmd.Stdout = outputFileHandle

	// Run the command
	if err := cmd.Run(); err != nil {
		return errors.Wrap(ctx, err, "decompress archive")
	}

	return nil
}

// appendArchiveToOutput appends the contents of sourceFile to destFile
func appendArchiveToOutput(ctx context.Context, sourceFile, destFile string) error {
	// Open source file for reading
	src, err := os.Open(sourceFile)
	if err != nil {
		return errors.Wrap(ctx, err, "open source file")
	}
	defer src.Close()

	// Open destination file for appending
	dest, err := os.OpenFile(destFile, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0644)
	if err != nil {
		return errors.Wrap(ctx, err, "open destination file")
	}
	defer dest.Close()

	// Copy the content
	_, err = io.Copy(dest, src)
	if err != nil {
		return errors.Wrap(ctx, err, "copy file content")
	}

	return nil
}
