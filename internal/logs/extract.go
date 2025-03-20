package logs

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/briceamen/logaround/internal/archive"
	"github.com/briceamen/logaround/internal/timestamp"
	"github.com/briceamen/logaround/internal/ui"
)

// ExtractLogs extracts logs for the specified app around a target timestamp
func ExtractLogs(appName, targetTimestamp string, lineCount int, hoursCount int) (string, error) {
	// Create timestamped output file
	ts := time.Now().Format("20060102-150405")
	outputDir := filepath.Join(os.TempDir(), "scalingo-logs")
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return "", fmt.Errorf("create output directory: %w", err)
	}

	// Temporary file for unsorted logs
	tempOutputFile := filepath.Join(outputDir, fmt.Sprintf("%s-%s-unsorted.log", appName, ts))

	// Final sorted output file
	outputFile := filepath.Join(outputDir, fmt.Sprintf("%s-%s.log", appName, ts))

	// Parse target timestamp if provided (for archive filtering)
	var targetTime time.Time
	var err error
	if targetTimestamp != "" {
		targetTime, err = timestamp.ParseSearch(targetTimestamp)
		if err != nil {
			return "", fmt.Errorf("parse target timestamp: %w", err)
		}
	}

	// Start spinner
	spinner := ui.NewSpinner("Fetching logs...")
	spinner.Start()
	defer spinner.Stop()

	// Get recent logs
	fmt.Println("Fetching recent logs for " + appName + "...")
	spinner.Update("Fetching recent logs...")
	if err := fetchRecentLogs(appName, tempOutputFile); err != nil {
		return "", err
	}

	// Get archived logs
	fmt.Println("Fetching archived logs for " + appName + "...")
	spinner.Update("Fetching archived logs...")
	if err := archive.FetchArchived(appName, outputDir, tempOutputFile, targetTime, spinner); err != nil {
		return "", err
	}

	// Sort the logs by timestamp
	fmt.Println("Sorting logs by timestamp...")
	spinner.Update("Sorting logs by timestamp...")
	if err := SortByTimestamp(tempOutputFile, outputFile); err != nil {
		return "", err
	}

	// If targeting a specific timestamp, filter logs
	if targetTimestamp != "" {
		if hoursCount > 0 {
			// Filter by hours around the timestamp
			fmt.Printf("Filtering logs around timestamp: %s (±%d hours)...\n", targetTimestamp, hoursCount)
			spinner.Update("Filtering logs around target timestamp by hours...")
			filterOutputFile := filepath.Join(outputDir, fmt.Sprintf("%s-%s-filtered.log", appName, ts))
			if err := FilterByHours(outputFile, filterOutputFile, targetTimestamp, hoursCount, appName); err != nil {
				return "", err
			}
			outputFile = filterOutputFile
		} else if lineCount > 0 {
			// Filter by lines around the timestamp
			fmt.Printf("Filtering logs around timestamp: %s (±%d lines)...\n", targetTimestamp, lineCount)
			spinner.Update("Filtering logs around target timestamp by lines...")
			filterOutputFile := filepath.Join(outputDir, fmt.Sprintf("%s-%s-filtered.log", appName, ts))
			if err := FilterByTimestamp(outputFile, filterOutputFile, targetTimestamp, lineCount, appName); err != nil {
				return "", err
			}
			outputFile = filterOutputFile
		}
	}

	// Clean up temporary files
	os.Remove(tempOutputFile)

	return outputFile, nil
}

// fetchRecentLogs fetches the most recent logs for the app
func fetchRecentLogs(appName, outputFile string) error {
	cmd := exec.Command("scalingo", "--app", appName, "logs", "-n", "10000000")

	// Open the output file
	file, err := os.Create(outputFile)
	if err != nil {
		return fmt.Errorf("create output file: %w", err)
	}
	defer file.Close()

	cmd.Stdout = file
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("execute scalingo logs command: %w", err)
	}

	return nil
}
