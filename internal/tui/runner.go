package tui

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/Scalingo/go-utils/errors/v2"
	tea "github.com/charmbracelet/bubbletea"
)

// RunExtractor runs a Bubbletea-based UI for log extraction
func RunExtractor(ctx context.Context, appName, targetTimestamp string, extractFunc func(chan StatusMessage, chan ErrorMessage, chan FinishMessage) error) error {
	// Create channels for communication
	statusCh := make(chan StatusMessage)
	errorCh := make(chan ErrorMessage)
	finishCh := make(chan FinishMessage)

	// Run the extraction in a separate goroutine
	go func() {
		if err := extractFunc(statusCh, errorCh, finishCh); err != nil {
			errorCh <- ErrorMessage{Error: err}
		}
	}()

	// Create and initialize the Bubbletea model
	model := NewModel(appName, targetTimestamp)
	// Add the title as a progress entry
	model.progressEntries = append(model.progressEntries, ProgressEntry{
		Message: titleStyle.Render("SCALILOGS"),
		Value:   0,
	})

	p := tea.NewProgram(model)

	// Handle status updates in a separate goroutine
	go func() {
		for {
			select {
			case status := <-statusCh:
				p.Send(status)
			case err := <-errorCh:
				p.Send(err)
				return
			case finish := <-finishCh:
				p.Send(finish)
				return
			case <-ctx.Done():
				return
			}
		}
	}()

	// Run the Bubbletea program
	if _, err := p.Run(); err != nil {
		return errors.Wrap(ctx, err, "run bubbletea program")
	}

	return nil
}

// UpdateStatus sends a status update to the channel
func UpdateStatus(statusCh chan<- StatusMessage, status string, value ...int) {
	val := 0
	if len(value) > 0 {
		val = value[0]
	}

	select {
	case statusCh <- StatusMessage{Status: status, Value: val}:
		// Sent successfully
	default:
		// Channel is full or closed, but don't print directly as that would
		// interfere with the Bubble Tea UI. Just drop the message in this case.
	}
}

// ReportError sends an error to the channel
func ReportError(errorCh chan<- ErrorMessage, ctx context.Context, err error, message string) error {
	wrappedErr := errors.Wrap(ctx, err, message)

	select {
	case errorCh <- ErrorMessage{Error: wrappedErr}:
		// Sent successfully
	default:
		// Channel is full or closed, print to stderr as a fallback
		fmt.Fprintf(os.Stderr, "Error: %v\n", wrappedErr)
	}

	return wrappedErr
}

// FinishProcess sends the final completion message to the channel
func FinishProcess(finishCh chan<- FinishMessage, outputFile string, liveLogsCount, archiveLogsCount, totalLines, filteredLineCount int, archiveDetails map[string]int, elapsedTime time.Duration) {
	select {
	case finishCh <- FinishMessage{
		OutputFile:        outputFile,
		LiveLogsCount:     liveLogsCount,
		ArchiveLogsCount:  archiveLogsCount,
		TotalLines:        totalLines,
		FilteredLineCount: filteredLineCount,
		ArchiveDetails:    archiveDetails,
		ElapsedTime:       elapsedTime.Round(time.Millisecond).String(),
	}:
		// Sent successfully
	default:
		// Channel is full or closed, print to stderr as a fallback
		fmt.Fprintf(os.Stderr, "Logs extraction complete: %s\n", outputFile)
	}
}
