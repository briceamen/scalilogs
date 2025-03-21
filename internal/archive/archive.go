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
	"github.com/briceamen/logaround/internal/ui"
	"github.com/briceamen/logaround/pkg/scalingo"
)

// FetchArchived fetches archived logs for the specified app
func FetchArchived(ctx context.Context, client *scalingo.Client, appName, outputDir, mainOutputFile string, targetTime time.Time, spinner *ui.Spinner) (int, map[string]int, error) {
	fmt.Printf("\nFetching archived logs for %s...\n", appName)

	totalLines := 0
	archiveDetails := make(map[string]int)

	// Get list of archives
	archivesResp, err := scalingo.FetchLogsArchives(ctx, client, appName)
	if err != nil {
		return 0, nil, errors.Wrap(ctx, err, "get logs archives list")
	}

	// Check if there are no archives available
	if len(archivesResp.Archives) == 0 {
		fmt.Println("No log archives available for this application.")
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
			fmt.Fprintf(os.Stderr, "Warning: Failed to parse From date: %v\n", err)
			continue
		}

		toTime, err := time.Parse("Mon Jan 2 15:04:05 -0700 MST 2006", archive.To)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: Failed to parse To date: %v\n", err)
			continue
		}

		processedArchives = append(processedArchives, ProcessedArchive{
			ArchiveItem: archive,
			FromTime:    fromTime,
			ToTime:      toTime,
			Index:       i,
		})
	}

	// Sort archives by date (newest first for more efficient filtering)
	sort.Slice(processedArchives, func(i, j int) bool {
		return processedArchives[i].ToTime.After(processedArchives[j].ToTime)
	})

	// Filter archives if a target time is provided
	var relevantArchives []ProcessedArchive
	if !targetTime.IsZero() {
		// For log line filtering, we need a smaller time buffer that's sufficient
		// for finding ~1000 lines before and after the target timestamp
		// Assuming 1 line per second on average as a conservative estimate
		// This is about 15-20 minutes before and after
		timeBuffer := 20 * time.Minute
		startBuffer := targetTime.Add(-timeBuffer)
		endBuffer := targetTime.Add(timeBuffer)

		// Check if the target time falls within any archives
		targetTimeInArchive := false
		var containingArchive *ProcessedArchive

		for i, archive := range processedArchives {
			if (archive.FromTime.Before(targetTime) || archive.FromTime.Equal(targetTime)) &&
				(archive.ToTime.After(targetTime) || archive.ToTime.Equal(targetTime)) {
				targetTimeInArchive = true
				containingArchive = &processedArchives[i]
				break
			}
		}

		// If the target time is found in an archive, use just that one
		if targetTimeInArchive && containingArchive != nil {
			fmt.Printf("Found archive containing target time %s (from %s to %s)\n",
				targetTime.Format("2006-01-02 15:04:05"),
				containingArchive.FromTime.Format("2006-01-02 15:04:05"),
				containingArchive.ToTime.Format("2006-01-02 15:04:05"))
			relevantArchives = append(relevantArchives, *containingArchive)
		} else {
			// First, find archives that might overlap with our buffer period
			for _, archive := range processedArchives {
				// Check if archive time range overlaps with our target range
				if (archive.FromTime.Before(endBuffer) || archive.FromTime.Equal(endBuffer)) &&
					(archive.ToTime.After(startBuffer) || archive.ToTime.Equal(startBuffer)) {
					relevantArchives = append(relevantArchives, archive)
				}
			}

			// If no archives were found in the buffer period, get the closest ones
			if len(relevantArchives) == 0 {
				var beforeArchive, afterArchive *ProcessedArchive

				// Find the closest archive that ends before our target time
				for i, archive := range processedArchives {
					if archive.ToTime.Before(targetTime) {
						beforeArchive = &processedArchives[i]
						break // archives are sorted newest first
					}
				}

				// Find the closest archive that starts after our target time
				// Need to sort in reverse order for this
				sortedArchives := make([]ProcessedArchive, len(processedArchives))
				copy(sortedArchives, processedArchives)
				sort.Slice(sortedArchives, func(i, j int) bool {
					return sortedArchives[i].FromTime.Before(sortedArchives[j].FromTime)
				})

				for i, archive := range sortedArchives {
					if archive.FromTime.After(targetTime) {
						afterArchive = &sortedArchives[i]
						break
					}
				}

				// Add the archives before and after if they exist
				if beforeArchive != nil {
					relevantArchives = append(relevantArchives, *beforeArchive)
				}
				if afterArchive != nil {
					relevantArchives = append(relevantArchives, *afterArchive)
				}

				// Log warning about the gap
				if !targetTimeInArchive && beforeArchive != nil && afterArchive != nil {
					fmt.Printf("\nWARNING: The target time %s falls within a gap between archives:\n", targetTime.Format("2006-01-02 15:04:05"))
					fmt.Printf("  - Archive ending at:   %s\n", beforeArchive.ToTime.Format("2006-01-02 15:04:05"))
					fmt.Printf("  - Next archive starts: %s\n", afterArchive.FromTime.Format("2006-01-02 15:04:05"))
					fmt.Printf("  - Gap duration:        %s\n\n", afterArchive.FromTime.Sub(beforeArchive.ToTime))
				}
			}
		}

		if len(relevantArchives) > 0 {
			fmt.Printf("\nSelected %d archives that may contain logs around the target time.\n", len(relevantArchives))
		} else {
			fmt.Println("\nNo relevant archives found for the target time. Using recent logs only.")
		}
	} else {
		// If no target time, only get the most recent archive
		if len(processedArchives) > 0 {
			relevantArchives = append(relevantArchives, processedArchives[0])
			fmt.Println("No target time specified. Using only the most recent archive.")
		}
	}

	// Sort the relevant archives by date (oldest first) for processing
	sort.Slice(relevantArchives, func(i, j int) bool {
		return relevantArchives[i].FromTime.Before(relevantArchives[j].FromTime)
	})

	// Process selected archives in chronological order
	for i, archive := range relevantArchives {
		archiveFile := fmt.Sprintf("%s/%s-archive-%d.log", outputDir, appName, i)

		// Update spinner with current progress
		spinner.Update(fmt.Sprintf("Downloading archive %d/%d (%s - %s)",
			i+1, len(relevantArchives),
			archive.FromTime.Format("2006-01-02 15:04 MST"),
			archive.ToTime.Format("2006-01-02 15:04 MST")))

		// Download and extract archive
		if err := downloadArchive(ctx, client, archive.ArchiveItem.URL, archiveFile); err != nil {
			fmt.Fprintf(os.Stderr, "\nWarning: Failed to download archive %d: %v\n", i, err)
			continue
		}

		// Count lines in the archive
		lineCount, err := countLines(archiveFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: Failed to count lines in archive %d: %v\n", i, err)
		} else {
			totalLines += lineCount
			archiveKey := fmt.Sprintf("%s to %s",
				archive.FromTime.Format("2006-01-02 15:04"),
				archive.ToTime.Format("2006-01-02 15:04"))
			archiveDetails[archiveKey] = lineCount
		}

		// Append archive contents to main log file
		spinner.Update(fmt.Sprintf("Processing archive %d/%d", i+1, len(relevantArchives)))
		if err := appendFiles(archiveFile, mainOutputFile); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: Failed to append archive %d: %v\n", i, err)
		}

		// Clean up archive file
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
		return errors.Wrap(ctx, err, "decompress archive with zcat")
	}

	fmt.Printf("\nSuccessfully decompressed archive to %s\n", outputFile)
	return nil
}

// Append file contents from source to destination
func appendFiles(sourceFile, destFile string) error {
	// Open source file
	src, err := os.Open(sourceFile)
	if err != nil {
		return fmt.Errorf("open source file: %w", err)
	}
	defer src.Close()

	// Open destination file in append mode
	dst, err := os.OpenFile(destFile, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0644)
	if err != nil {
		return fmt.Errorf("open destination file: %w", err)
	}
	defer dst.Close()

	// Append source content to destination
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
