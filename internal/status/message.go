package status

import (
	"context"
	"fmt"
	"os"
)

// StatusMessage is used to update UI components with new status
type StatusMessage struct {
	Status string
	Value  int
}

// ErrorMessage is used to report errors
type ErrorMessage struct {
	Error error
}

// FinishMessage is sent when processing is complete
type FinishMessage struct {
	OutputFile        string
	LiveLogsCount     int
	ArchiveLogsCount  int
	TotalLines        int
	FilteredLineCount int
	ArchiveDetails    map[string]int
	ElapsedTime       string
}

// Update sends a status update to the channel
func UpdateStatus(statusCh chan<- StatusMessage, status string, value ...int) {
	if statusCh == nil {
		return
	}

	val := 0
	if len(value) > 0 {
		val = value[0]
	}

	select {
	case statusCh <- StatusMessage{Status: status, Value: val}:
		// Sent successfully
	default:
		// Do nothing
	}
}

func ReportError(ctx context.Context, errorCh chan<- ErrorMessage, error error) error {
	select {
	case errorCh <- ErrorMessage{Error: error}:
		// Sent successfully
	default:
		fmt.Fprintf(os.Stderr, "Error report failed: %s\n", error)
	}
	return error
}
