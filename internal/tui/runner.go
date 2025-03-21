package tui

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/Scalingo/go-utils/errors/v2"
	"github.com/briceamen/scalilogs/internal/status"
	tea "github.com/charmbracelet/bubbletea"
)

// RunExtractor runs a Bubbletea-based UI for log extraction
func RunExtractor(ctx context.Context, appName, targetTimestamp string, extractFunc func(chan status.StatusMessage, chan status.ErrorMessage, chan status.FinishMessage) error) error {
	// Create channels for communication
	statusCh := make(chan status.StatusMessage)
	errorCh := make(chan status.ErrorMessage)
	finishCh := make(chan status.FinishMessage)

	// Run the extraction in a separate goroutine
	go func() {
		if err := extractFunc(statusCh, errorCh, finishCh); err != nil {
			errorCh <- status.ErrorMessage{Error: err}
		}
	}()

	// Create and initialize the Bubbletea model
	model := NewModel(appName, targetTimestamp)
	// Add the title as a progress entry
	model.progressEntries = append(model.progressEntries, ProgressEntry{
		Message: titleStyle.Render("Scalilogs"),
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

// ReportError sends an error to the channel
func ReportError(errorCh chan<- status.ErrorMessage, ctx context.Context, err error, message string) error {
	wrappedErr := errors.Wrap(ctx, err, message)

	select {
	case errorCh <- status.ErrorMessage{Error: wrappedErr}:
		// Sent successfully
	default:
		// Channel is full or closed, print to stderr as a fallback
		fmt.Fprintf(os.Stderr, "Error: %v\n", wrappedErr)
	}

	return wrappedErr
}

// FinishProcess sends the final completion message to the channel
func FinishProcess(finishCh chan<- status.FinishMessage, outputFile string, liveLogsCount, archiveLogsCount, totalLines, filteredLineCount int, archiveDetails map[string]int, elapsedTime time.Duration) {
	select {
	case finishCh <- status.FinishMessage{
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
