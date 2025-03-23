package archive

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"sort"
	"time"

	"github.com/Scalingo/go-utils/errors/v2"
	"github.com/briceamen/scalilogs/internal/status"
	"github.com/briceamen/scalilogs/pkg/scalingo"
)

// FetchLogsArchives returns a list of log archives for the specified app
// This is exported as a variable so it can be mocked in tests
var FetchLogsArchives = fetchLogsArchives

// fetchLogsArchives is the actual implementation
func fetchLogsArchives(ctx context.Context, statusCh chan<- status.Message, client *scalingo.Client, appName string) (*scalingo.LogsArchivesResponse, error) {

	// Get first page without cursor
	archivesList, err := client.Scalingo.LogsArchivesByCursor(ctx, appName, "")
	if err != nil {
		return nil, errors.Wrap(ctx, err, "get logs archives list (first page)")
	}

	// Store all archives
	archives := archivesList.Archives
	pageCount := 1

	// Fetch ALL available pages using cursor-based pagination to ensure we have the complete history
	// Continue until HasMore is false or we've fetched an unreasonable number of pages
	maxPagesFetch := 1000 // Very high limit to ensure we exhaust all available archives

	for archivesList.HasMore && pageCount < maxPagesFetch {
		// Use the next_cursor value to get the next page
		nextArchivesList, err := client.Scalingo.LogsArchivesByCursor(ctx, appName, archivesList.NextCursor)
		if err != nil {
			// Log the error but continue with what we have
			fmt.Printf("WARNING: Failed to fetch logs archives page with cursor %s: %v\n",
				archivesList.NextCursor, err)
			break
		}

		// Append archives from this page
		archives = append(archives, nextArchivesList.Archives...)

		// Update for next iteration
		archivesList.HasMore = nextArchivesList.HasMore
		archivesList.NextCursor = nextArchivesList.NextCursor
		pageCount++

		// Status update for user during long fetches
		if pageCount%5 == 0 {
			// Send consistent progress info format
			status.Update(statusCh, fmt.Sprintf("Fetching archives, found %d so far", len(archives)))
		}
	}

	// Report whether we successfully retrieved all archives or hit the page limit through status channel
	if !archivesList.HasMore {
		// We successfully retrieved all available archives
		status.Update(statusCh, fmt.Sprintf("Retrieved all archives (%d)", len(archives)))
	} else if pageCount >= maxPagesFetch {
		// We hit the page limit, which shouldn't happen in practice
		status.Update(statusCh, fmt.Sprintf("WARNING: Hit page limit (%d)", maxPagesFetch))
	}

	// Create a new response with all archives
	return &scalingo.LogsArchivesResponse{
		Archives:   archives,
		NextCursor: archivesList.NextCursor,
		HasMore:    archivesList.HasMore, // Preserve original HasMore value
	}, nil
}

// FetchArchived fetches archived logs for the specified app
func FetchArchived(ctx context.Context, client *scalingo.Client, appName, outputDir, mainOutputFile string, targetTime time.Time, statusCh chan<- status.Message, hoursCount int, lineCount int) (int, map[string]int, error) {
	status.Update(statusCh, "fetching archived logs for "+appName)

	totalLines := 0
	archiveDetails := make(map[string]int)

	// Get list of archives
	archivesResp, err := FetchLogsArchives(ctx, statusCh, client, appName)
	if err != nil {
		return 0, nil, errors.Wrap(ctx, err, "get logs archives list")
	}

	// Give feedback on the number of archives found
	status.Update(statusCh, fmt.Sprintf("found %d log archives, analyzing timestamps...", len(archivesResp.Archives)))

	// Check if there are no archives available
	if len(archivesResp.Archives) == 0 {
		status.Update(statusCh, "no log archives available for this application")
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
		// Sort archives by time (newest first by default)
		if targetTime.Year() < time.Now().Year()-1 {
			// For very old timestamps (more than a year ago), sort oldest first
			// This improves search performance for historical logs
			sort.Slice(processedArchives, func(i, j int) bool {
				return processedArchives[i].FromTime.Before(processedArchives[j].FromTime)
			})
			status.Update(statusCh, fmt.Sprintf("searching for older timestamp (%s), prioritizing older archives first...",
				targetTime.Format("2006-01-02 15:04:05")))
		} else {
			// For recent timestamps, sort newest first (default behavior)
			sort.Slice(processedArchives, func(i, j int) bool {
				return processedArchives[i].ToTime.After(processedArchives[j].ToTime)
			})
		}

		// Try to find archives that contain or overlap with the target time
		// If target time is within an archive's range, include it
		foundExact := false
		for _, archive := range processedArchives {
			// Check if the target time is within this archive's range
			if (targetTime.Equal(archive.FromTime) || targetTime.After(archive.FromTime)) &&
				(targetTime.Equal(archive.ToTime) || targetTime.Before(archive.ToTime)) {
				// Found exact match
				relevantArchives = append(relevantArchives, archive)
				foundExact = true

				statusMsg := fmt.Sprintf("found archive containing target time %s (from %s to %s)",
					targetTime.Format("2006-01-02 15:04:05"),
					archive.FromTime.Format("2006-01-02 15:04:05"),
					archive.ToTime.Format("2006-01-02 15:04:05"))
				status.Update(statusCh, statusMsg)

				// Don't break - we need to continue looking for adjacent archives
				// that might be relevant for time range queries
			}
		}

		// If filtering by hours or lines range, we need to check additional archives
		// that may contain logs within that time range
		isHoursFiltering := hoursCount > 0
		isLinesFiltering := lineCount > 0

		// If we're filtering by hours/lines or we didn't find an exact match
		if isHoursFiltering || isLinesFiltering || !foundExact {
			// Look for archives that may contain logs within the time range
			// Calculate appropriate time range based on parameters
			var timeRange time.Duration

			if isHoursFiltering {
				// Use specified hours
				timeRange = time.Duration(hoursCount) * time.Hour
			} else if isLinesFiltering {
				// Estimate time range from line count (assuming ~1 line per second)
				// Add a 50% buffer to be safe
				estimatedSeconds := lineCount * 3 / 2
				timeRange = time.Duration(estimatedSeconds) * time.Second

				// Ensure a minimum time range of 5 minutes
				if timeRange < 5*time.Minute {
					timeRange = 5 * time.Minute
				}

				// And a maximum of 12 hours to avoid excessive data retrieval
				if timeRange > 12*time.Hour {
					timeRange = 12 * time.Hour
				}

				// Inform user about estimation
				estimationMsg := fmt.Sprintf("estimated time range from %d lines: %s (±%s)",
					lineCount,
					formatDuration(timeRange/2),
					formatDuration(timeRange/2))
				status.Update(statusCh, estimationMsg)
			} else {
				// Default for when we just didn't find an exact match
				timeRange = 24 * time.Hour
			}

			rangeStartTime := targetTime.Add(-timeRange)
			rangeEndTime := targetTime.Add(timeRange)

			status.Update(statusCh, fmt.Sprintf("searching for archives in time range: %s to %s",
				rangeStartTime.Format("2006-01-02 15:04:05"),
				rangeEndTime.Format("2006-01-02 15:04:05")))

			for _, archive := range processedArchives {
				// Skip if we already added this archive (exact match)
				alreadyIncluded := false
				for _, included := range relevantArchives {
					if archive.ArchiveItem.URL == included.ArchiveItem.URL {
						alreadyIncluded = true
						break
					}
				}
				if alreadyIncluded {
					continue
				}

				// Include archive if it overlaps with our time range at all
				// Archive overlaps with range if:
				// - Archive starts before range ends AND
				// - Archive ends after range starts
				if (archive.FromTime.Before(rangeEndTime) || archive.FromTime.Equal(rangeEndTime)) &&
					(archive.ToTime.After(rangeStartTime) || archive.ToTime.Equal(rangeStartTime)) {
					relevantArchives = append(relevantArchives, archive)

					// Check if archive fully contains the target range, partially overlaps,
					// or is completely inside the target range
					var overlapType string
					if (archive.FromTime.Before(rangeStartTime) || archive.FromTime.Equal(rangeStartTime)) &&
						(archive.ToTime.After(rangeEndTime) || archive.ToTime.Equal(rangeEndTime)) {
						overlapType = "fully contains"
					} else if archive.FromTime.After(rangeStartTime) && archive.ToTime.Before(rangeEndTime) {
						overlapType = "completely inside"
					} else {
						overlapType = "partially overlaps with"
					}

					statusMsg := fmt.Sprintf("including archive that %s time range: %s to %s",
						overlapType,
						archive.FromTime.Format("2006-01-02 15:04:05"),
						archive.ToTime.Format("2006-01-02 15:04:05"))
					status.Update(statusCh, statusMsg)
				}
			}
		}

		// If we still haven't found any relevant archives
		if len(relevantArchives) == 0 {
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
					status.Update(statusCh, statusMsg)

					beforeMsg := fmt.Sprintf("archive ending at: %s",
						beforeArchive.ToTime.Format("2006-01-02 15:04:05"))
					status.Update(statusCh, beforeMsg)

					afterMsg := fmt.Sprintf("next archive starts: %s",
						afterArchive.FromTime.Format("2006-01-02 15:04:05"))
					status.Update(statusCh, afterMsg)

					gapMsg := fmt.Sprintf("gap duration: %s",
						afterArchive.FromTime.Sub(beforeArchive.ToTime))
					status.Update(statusCh, gapMsg)
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
			status.Update(statusCh, "no target time specified, using most recent archive")
		}
	}

	// Report how many archives we're using
	if len(relevantArchives) > 0 {
		// Sort relevant archives by their start time (oldest first)
		// This ensures logs are processed in chronological order
		sort.Slice(relevantArchives, func(i, j int) bool {
			return relevantArchives[i].FromTime.Before(relevantArchives[j].FromTime)
		})

		statusMsg := fmt.Sprintf("selected %d archives that may contain logs around the target time",
			len(relevantArchives))
		status.Update(statusCh, statusMsg)
	} else {
		status.Update(statusCh, "no relevant archives found for the target time, using recent logs only")
		return 0, archiveDetails, nil
	}

	// Download and process each relevant archive
	for i, archive := range relevantArchives {
		archiveUrl := archive.ArchiveItem.URL
		// Download the archive with progress UI
		archiveFile, err := CreateDecompressedArchive(ctx, client, archiveUrl, appName, outputDir, i)
		if err != nil {
			return totalLines, archiveDetails, errors.Wrap(ctx, err, "download and decompress archive")
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
		processingMsg := fmt.Sprintf("processing archive %d/%d %s",
			i+1, len(relevantArchives), appName)
		status.Update(statusCh, processingMsg)

		// Append to main output file
		if err := appendFiles(archiveFile, mainOutputFile); err != nil {
			return totalLines, archiveDetails, errors.Wrap(ctx, err, "append archive to output")
		}

		// Clean up the archive file
		os.Remove(archiveFile)
	}

	return totalLines, archiveDetails, nil
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

// formatDuration formats a duration in a user-friendly way
func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%d seconds", int(d.Seconds()))
	} else if d < time.Hour {
		return fmt.Sprintf("%d minutes", int(d.Minutes()))
	} else {
		hours := int(d.Hours())
		minutes := int(d.Minutes()) % 60
		if minutes > 0 {
			return fmt.Sprintf("%d hours %d minutes", hours, minutes)
		}
		return fmt.Sprintf("%d hours", hours)
	}
}
