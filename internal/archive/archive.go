package archive

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/briceamen/logaround/internal/ui"
)

// FetchArchived fetches archived logs for the specified app
func FetchArchived(appName, outputDir, mainOutputFile string, targetTime time.Time, spinner *ui.Spinner) error {
	fmt.Printf("Fetching archived logs for %s...\n", appName)

	// Get list of archives
	cmd := exec.Command("scalingo", "--app", appName, "logs-archives")
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("get logs archives list: %w", err)
	}

	// Check if there are no archives available
	if strings.Contains(string(output), "No logs archives available") {
		fmt.Println("No log archives available for this application.")
		return nil
	}

	// Parse output to find archive information
	archives, err := parseArchivesOutput(string(output))
	if err != nil {
		return fmt.Errorf("parse archives output: %w", err)
	}

	// Sort archives by date (newest first for more efficient filtering)
	sort.Slice(archives, func(i, j int) bool {
		return archives[i].ToTime.After(archives[j].ToTime)
	})

	// Filter archives if a target time is provided
	var relevantArchives []ArchiveInfo
	if !targetTime.IsZero() {
		// Define a wider time buffer to account for potential gaps between archives
		// 12 hours before and after to ensure we capture all relevant data
		timeBuffer := 12 * time.Hour
		startBuffer := targetTime.Add(-timeBuffer)
		endBuffer := targetTime.Add(timeBuffer)

		// Check if the target time falls within any archives
		targetTimeInArchive := false
		for _, archive := range archives {
			if (archive.FromTime.Before(targetTime) || archive.FromTime.Equal(targetTime)) &&
				(archive.ToTime.After(targetTime) || archive.ToTime.Equal(targetTime)) {
				targetTimeInArchive = true
				break
			}
		}

		// First, find archives that might contain the target time
		for _, archive := range archives {
			// Check if archive time range overlaps with our target range
			if (archive.FromTime.Before(endBuffer) || archive.FromTime.Equal(endBuffer)) &&
				(archive.ToTime.After(startBuffer) || archive.ToTime.Equal(startBuffer)) {
				relevantArchives = append(relevantArchives, archive)
			}
		}

		// If no archives were found and target time isn't in any archive, get surrounding archives
		if len(relevantArchives) == 0 {
			var beforeArchive, afterArchive *ArchiveInfo

			// Find the closest archive that ends before our target time
			for i, archive := range archives {
				if archive.ToTime.Before(targetTime) {
					beforeArchive = &archives[i]
					break // archives are sorted newest first
				}
			}

			// Find the closest archive that starts after our target time
			// Need to sort in reverse order for this
			sortedArchives := make([]ArchiveInfo, len(archives))
			copy(sortedArchives, archives)
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
	} else {
		// If no target time, use all archives
		relevantArchives = archives
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
		if err := downloadAndExtract(archive.URL, archiveFile); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: Failed to download archive %d: %v\n", i, err)
			continue
		}

		// Append archive contents to main log file
		spinner.Update(fmt.Sprintf("Processing archive %d/%d", i+1, len(relevantArchives)))
		if err := appendFiles(archiveFile, mainOutputFile); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: Failed to append archive %d: %v\n", i, err)
		}

		// Clean up archive file
		os.Remove(archiveFile)
	}

	return nil
}

// Parse the output of the 'scalingo logs-archives' command
func parseArchivesOutput(output string) ([]ArchiveInfo, error) {
	var archives []ArchiveInfo
	var currentArchive ArchiveInfo

	// Handle empty or invalid output
	if len(strings.TrimSpace(output)) == 0 || strings.Contains(output, "No logs archives available") {
		return []ArchiveInfo{}, nil
	}

	lines := strings.Split(output, "\n")
	index := 0

	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])

		if line == "" {
			continue
		}

		if line == "-------" {
			// End of current archive info, add to list if valid
			if currentArchive.URL != "" && !currentArchive.FromTime.IsZero() && !currentArchive.ToTime.IsZero() {
				currentArchive.Index = index
				archives = append(archives, currentArchive)
				index++
				currentArchive = ArchiveInfo{} // Reset for next archive
			}
			continue
		}

		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}

		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])

		switch key {
		case "To":
			t, err := time.Parse("Mon Jan 2 15:04:05 -0700 MST 2006", value)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Warning: Failed to parse To date: %v\n", err)
			} else {
				currentArchive.ToTime = t
			}
		case "From":
			t, err := time.Parse("Mon Jan 2 15:04:05 -0700 MST 2006", value)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Warning: Failed to parse From date: %v\n", err)
			} else {
				currentArchive.FromTime = t
			}
		case "Size":
			currentArchive.Size = value
		case "Url":
			currentArchive.URL = value
			// Extract archive ID from URL if possible
			if idStart := strings.Index(value, "download/"); idStart > 0 {
				idStart += len("download/")
				if idEnd := strings.Index(value[idStart:], "."); idEnd > 0 {
					currentArchive.ArchiveID = value[idStart : idStart+idEnd]
				}
			}
		}
	}

	// Check if we need to add the last archive
	if currentArchive.URL != "" && !currentArchive.FromTime.IsZero() && !currentArchive.ToTime.IsZero() {
		currentArchive.Index = index
		archives = append(archives, currentArchive)
	}

	// Return empty slice instead of error when no archives are found
	if len(archives) == 0 {
		return []ArchiveInfo{}, nil
	}

	return archives, nil
}

// Download and extract an archive using wget and zcat
func downloadAndExtract(url, outputFile string) error {
	// Download with wget
	wgetCmd := exec.Command("wget", "-qO-", url)

	// Pipe to zcat
	zcatCmd := exec.Command("zcat")

	var err error
	zcatCmd.Stdin, err = wgetCmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("connect wget to zcat: %w", err)
	}

	// Write to file
	outFile, err := os.Create(outputFile)
	if err != nil {
		return fmt.Errorf("create archive output file: %w", err)
	}
	defer outFile.Close()

	zcatCmd.Stdout = outFile

	// Start commands
	if err := zcatCmd.Start(); err != nil {
		return fmt.Errorf("start zcat: %w", err)
	}

	if err := wgetCmd.Run(); err != nil {
		return fmt.Errorf("download archive: %w", err)
	}

	if err := zcatCmd.Wait(); err != nil {
		return fmt.Errorf("extract archive: %w", err)
	}

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
	dst, err := os.OpenFile(destFile, os.O_APPEND|os.O_WRONLY, 0644)
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
