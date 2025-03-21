package archive

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"time"

	"github.com/Scalingo/go-utils/errors/v2"
	"github.com/briceamen/scalilogs/internal/tui"
	"github.com/briceamen/scalilogs/pkg/scalingo"
)

// FetchArchived fetches archived logs for the specified app
func FetchArchived(ctx context.Context, client *scalingo.Client, appName, outputDir, mainOutputFile string, targetTime time.Time, statusCh chan<- tui.StatusMessage) (int, map[string]int, error) {
	tui.UpdateStatus(statusCh, "fetching archived logs for "+appName)

	totalLines := 0
	archiveDetails := make(map[string]int)

	// Get list of archives
	archivesResp, err := scalingo.FetchLogsArchives(ctx, client, appName)
	if err != nil {
		return 0, nil, errors.Wrap(ctx, err, "get logs archives list")
	}

	// Check if there are no archives available
	if len(archivesResp.Archives) == 0 {
		tui.UpdateStatus(statusCh, "no log archives available for this application")
		return 0, archiveDetails, nil
	}

	// Process archives and their time ranges
	type ProcessedArchive struct {
		ArchiveItem scalingo.LogsArchiveItem
		FromTime    time.Time
		ToTime      time.Time
		Index       int
	}

	// Process and parse the timestamps from the archives
	processedArchives := make([]ProcessedArchive, 0, len(archivesResp.Archives))
	for i, archive := range archivesResp.Archives {
		// Parse timestamps from strings using the correct format
		// Format: "Mon Jan 2 15:04:05 -0700 MST 2006"
		fromTime, err := time.Parse("Mon Jan 2 15:04:05 -0700 MST 2006", archive.From)
		if err != nil {
			return 0, nil, errors.Wrap(ctx, err, "parse archive from time")
		}

		toTime, err := time.Parse("Mon Jan 2 15:04:05 -0700 MST 2006", archive.To)
		if err != nil {
			return 0, nil, errors.Wrap(ctx, err, "parse archive to time")
		}

		processedArchives = append(processedArchives, ProcessedArchive{
			ArchiveItem: archive,
			FromTime:    fromTime,
			ToTime:      toTime,
			Index:       i,
		})
	}

	// Find relevant archives based on the target time
	var relevantArchives []ProcessedArchive

	if !targetTime.IsZero() {
		// Sort archives by time (newest first)
		sort.Slice(processedArchives, func(i, j int) bool {
			return processedArchives[i].ToTime.After(processedArchives[j].ToTime)
		})

		// Try to find an archive that contains the target time
		found := false
		for _, archive := range processedArchives {
			// Check if the target time is within this archive's range
			if (targetTime.Equal(archive.FromTime) || targetTime.After(archive.FromTime)) &&
				(targetTime.Equal(archive.ToTime) || targetTime.Before(archive.ToTime)) {
				// Found exact match
				relevantArchives = append(relevantArchives, archive)
				found = true

				statusMsg := fmt.Sprintf("found archive containing target time %s (from %s to %s)",
					targetTime.Format("2006-01-02 15:04:05"),
					archive.FromTime.Format("2006-01-02 15:04:05"),
					archive.ToTime.Format("2006-01-02 15:04:05"))
				tui.UpdateStatus(statusCh, statusMsg)
				break
			}
		}

		if !found {
			// If not exact match, try to find archives closest to the target time
			// Find the archive that ends right before the target time
			var beforeArchive *ProcessedArchive
			var afterArchive *ProcessedArchive

			for i, archive := range processedArchives {
				if archive.ToTime.Before(targetTime) {
					beforeArchive = &processedArchives[i]
					break
				}
			}

			// Find the archive that starts right after the target time
			// Since we sorted newest first, we need to go backwards
			for i := len(processedArchives) - 1; i >= 0; i-- {
				if processedArchives[i].FromTime.After(targetTime) {
					afterArchive = &processedArchives[i]
					break
				}
			}

			// If we have archives before and after, check the gap
			if beforeArchive != nil && afterArchive != nil {
				// If the gap is relatively small (less than 2 hours), include both
				gap := afterArchive.FromTime.Sub(beforeArchive.ToTime)
				if gap < 2*time.Hour {
					relevantArchives = append(relevantArchives, *beforeArchive, *afterArchive)
				} else {
					// Large gap, just use the one closest to target time
					beforeDiff := targetTime.Sub(beforeArchive.ToTime)
					afterDiff := afterArchive.FromTime.Sub(targetTime)
					if beforeDiff < afterDiff {
						relevantArchives = append(relevantArchives, *beforeArchive)
					} else {
						relevantArchives = append(relevantArchives, *afterArchive)
					}

					statusMsg := fmt.Sprintf("target time %s falls within a gap between archives",
						targetTime.Format("2006-01-02 15:04:05"))
					tui.UpdateStatus(statusCh, statusMsg)

					beforeMsg := fmt.Sprintf("archive ending at: %s",
						beforeArchive.ToTime.Format("2006-01-02 15:04:05"))
					tui.UpdateStatus(statusCh, beforeMsg)

					afterMsg := fmt.Sprintf("next archive starts: %s",
						afterArchive.FromTime.Format("2006-01-02 15:04:05"))
					tui.UpdateStatus(statusCh, afterMsg)

					gapMsg := fmt.Sprintf("gap duration: %s",
						afterArchive.FromTime.Sub(beforeArchive.ToTime))
					tui.UpdateStatus(statusCh, gapMsg)
				}
			} else if beforeArchive != nil {
				// Only have archive before target
				relevantArchives = append(relevantArchives, *beforeArchive)
			} else if afterArchive != nil {
				// Only have archive after target
				relevantArchives = append(relevantArchives, *afterArchive)
			}
		}
	} else {
		// No target time specified, just use the most recent archive
		if len(processedArchives) > 0 {
			// Sort by time (newest first)
			sort.Slice(processedArchives, func(i, j int) bool {
				return processedArchives[i].ToTime.After(processedArchives[j].ToTime)
			})
			relevantArchives = append(relevantArchives, processedArchives[0])
			tui.UpdateStatus(statusCh, "no target time specified, using most recent archive")
		}
	}

	// Report how many archives we're using
	if len(relevantArchives) > 0 {
		statusMsg := fmt.Sprintf("selected %d archives that may contain logs around the target time",
			len(relevantArchives))
		tui.UpdateStatus(statusCh, statusMsg)
	} else {
		tui.UpdateStatus(statusCh, "no relevant archives found for the target time, using recent logs only")
		return 0, archiveDetails, nil
	}

	// Download and process each relevant archive
	for i, archive := range relevantArchives {
		archiveUrl := archive.ArchiveItem.URL

		// Create a temporary file for this archive
		archiveFile := fmt.Sprintf("%s/%s-archive-%d.log", outputDir, appName, i)

		// Status message for downloading
		statusMsg := fmt.Sprintf("downloading archive %d/%d (%s - %s)",
			i+1, len(relevantArchives),
			archive.FromTime.Format("2006-01-02 15:04 MST"),
			archive.ToTime.Format("2006-01-02 15:04 MST"))
		tui.UpdateStatus(statusCh, statusMsg)

		// Download the archive
		if err := downloadArchive(ctx, client, archiveUrl, archiveFile); err != nil {
			return totalLines, archiveDetails, errors.Wrap(ctx, err, "download archive")
		}

		// Count lines in this archive
		archiveLineCount, err := countLines(archiveFile)
		if err != nil {
			return totalLines, archiveDetails, errors.Wrap(ctx, err, "count lines in archive")
		}

		// Add to the total count
		totalLines += archiveLineCount

		// Add to details
		archiveTimeKey := fmt.Sprintf("%s - %s",
			archive.FromTime.Format("2006-01-02 15:04"),
			archive.ToTime.Format("2006-01-02 15:04"))
		archiveDetails[archiveTimeKey] = archiveLineCount

		// Processing message
		processingMsg := fmt.Sprintf("processing archive %d/%dr %s",
			i+1, len(relevantArchives), appName)
		tui.UpdateStatus(statusCh, processingMsg)

		// Append to main output file
		if err := appendFiles(archiveFile, mainOutputFile); err != nil {
			return totalLines, archiveDetails, errors.Wrap(ctx, err, "append archive to output")
		}

		// Clean up the archive file
		os.Remove(archiveFile)
	}

	return totalLines, archiveDetails, nil
}

// Download and extract an archive using the Scalingo client
func downloadArchive(ctx context.Context, client *scalingo.Client, url, outputFile string) error {
	// Create temp file for compressed data
	tempGzFile := outputFile + ".gz"
	outTempFile, err := os.Create(tempGzFile)
	if err != nil {
		return errors.Wrap(ctx, err, "create temp gzip file")
	}
	defer func() {
		outTempFile.Close()
		os.Remove(tempGzFile) // Clean up temp file
	}()

	// Download archive using the Scalingo client (to temp file)
	if err := scalingo.DownloadLogsArchive(ctx, client, url, outTempFile); err != nil {
		return errors.Wrap(ctx, err, "download archive")
	}

	// Close the temp file so we can reopen it for reading
	outTempFile.Close()

	// Create the output file
	outputFileHandle, err := os.Create(outputFile)
	if err != nil {
		return errors.Wrap(ctx, err, "create output file")
	}
	defer outputFileHandle.Close()

	// Use zcat to decompress the gzip file
	cmd := exec.Command("zcat", tempGzFile)
	cmd.Stdout = outputFileHandle

	// Run the command
	if err := cmd.Run(); err != nil {
		return errors.Wrap(ctx, err, "decompress archive")
	}

	return nil
}

// appendFiles appends the contents of sourceFile to destFile
func appendFiles(sourceFile, destFile string) error {
	// Open source file for reading
	src, err := os.Open(sourceFile)
	if err != nil {
		return fmt.Errorf("open source file: %w", err)
	}
	defer src.Close()

	// Open destination file for appending
	dst, err := os.OpenFile(destFile, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("open destination file: %w", err)
	}
	defer dst.Close()

	// Copy content from source to destination
	if _, err := io.Copy(dst, src); err != nil {
		return fmt.Errorf("append file content: %w", err)
	}

	return nil
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
