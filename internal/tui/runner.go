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
func RunExtractor(ctx context.Context, appName, targetTimestamp string, extractFunc func(chan status.Message, chan status.FinishMessage) error) error {
	// Create channels for communication
	statusCh := make(chan status.Message)
	finishCh := make(chan status.FinishMessage)
	errorCh := make(chan status.ErrorMessage)

	// Track if an error was encountered
	var extractError error

	// Run the extraction in a separate goroutine
	go func() {
		if err := extractFunc(statusCh, finishCh); err != nil {
			extractError = err
			// Send the error through the status channel without wrapping it again
			errMsg := status.ReportError(ctx, statusCh, err)
			errorCh <- errMsg
		}
	}()

	// Create and initialize the Bubbletea model
	model := NewModel(appName, targetTimestamp)

	p := tea.NewProgram(model)

	// Handle status updates in a separate goroutine
	go func() {
		for {
			select {
			case status := <-statusCh:
				p.Send(status)
			case err := <-errorCh:
				// Store the error for later use
				extractError = err.Error
				// Send to the model
				p.Send(err)
				return
			case finish := <-finishCh:
				p.Send(finish)
				return
			case <-ctx.Done():
				// Context was cancelled, send error to model with clean message
				ctxErr := errors.New(ctx, "operation cancelled by user")
				errMsg := status.ReportError(ctx, statusCh, ctxErr)
				p.Send(errMsg)
				extractError = ctxErr
				return
			}
		}
	}()

	// Run the Bubbletea program
	if _, err := p.Run(); err != nil {
		return errors.New(ctx, "terminal UI error")
	}

	// If there was an error in the extraction process, return it
	if extractError != nil {
		// We don't need to wrap it again as it should already be properly wrapped
		return extractError
	}

	return nil
}

// FinishProcess sends the final completion message to the channel
func FinishProcess(finishCh chan<- status.FinishMessage, outputFile string, liveLogsCount, archiveLogsCount,
	totalLines, filteredLineCount int, archiveDetails map[string]int, elapsedTime time.Duration,
	archiveSelectionTime, fetchLiveTime, fetchArchiveTime, sortTime, filterTime time.Duration) {

	select {
	case finishCh <- status.FinishMessage{
		OutputFile:        outputFile,
		LiveLogsCount:     liveLogsCount,
		ArchiveLogsCount:  archiveLogsCount,
		TotalLines:        totalLines,
		FilteredLineCount: filteredLineCount,
		ArchiveDetails:    archiveDetails,
		ElapsedTime:       elapsedTime.Round(time.Millisecond).String(),
		// Detailed timing information
		ArchiveSelectionTime: archiveSelectionTime.Round(time.Millisecond).String(),
		FetchLiveTime:        fetchLiveTime.Round(time.Millisecond).String(),
		FetchArchiveTime:     fetchArchiveTime.Round(time.Millisecond).String(),
		SortTime:             sortTime.Round(time.Millisecond).String(),
		FilterTime:           filterTime.Round(time.Millisecond).String(),
	}:
		// Sent successfully
	default:
		// Channel is full or closed, print to stderr as a fallback
		fmt.Fprintf(os.Stderr, "Logs extraction complete: %s\n", outputFile)
	}
}
