package status

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"strings"
	"time"
)

// Message is used to update UI components with new status
type Message struct {
	Status         string
	Value          int
	IsErrorMessage bool
}

// ErrorMessage is sent when an error occurs
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
	// Detailed timing information
	ArchiveSelectionTime string
	FetchLiveTime        string
	FetchArchiveTime     string
	SortTime             string
	FilterTime           string
}

// Update sends a status update to the channel
func Update(statusCh chan<- Message, status string, value ...int) {
	if statusCh == nil {
		return
	}

	val := 0
	if len(value) > 0 {
		val = value[0]
	}

	// Check if this is an archive progress message
	if strings.Contains(status, "pages with") ||
		strings.Contains(status, "archives across") ||
		strings.Contains(status, "Uncovered") ||
		strings.Contains(status, "downloading archive") ||
		(strings.Contains(status, "Page") && strings.Contains(status, "yielded")) {
	}

	select {
	case statusCh <- Message{
		Status: status,
		Value:  val,
	}:
		// Sent successfully
	default:
		// Do nothing
	}
}

// GetRandomWorkingMessage returns a random funny "we are working on it" message
func GetRandomWorkingMessage() string {
	messages := []string{
		"Feeding the hamster that powers the server",
		"Convincing the logs to reveal themselves",
		"Herding digital cats into orderly rows",
		"Untangling the spaghetti of your logs",
		"Brewing coffee for the overworked log parser",
		"Bargaining with the database for your data",
		"Converting coffee to log entries",
		"Politely asking your logs to form a queue",
		"Performing log wizardry, please wait",
		"Waking up the sleeping server hamsters",
		"Calculating the meaning of life and your logs",
		"Coaxing logs from their digital hiding places",
		"Playing hide and seek with your log files",
		"Translating binary into something useful",
		"Consulting the oracle of log wisdom",
		"Applying duck tape to broken log pipes",
		"Searching for logs in all the right places",
		"Telling the server that this is very important",
		"Reticulating splines and log entries",
		"Solving complex log algorithms with a crayon",
		"Interpreting ancient log hieroglyphics",
		"Mining for precious log nuggets",
		"Luring shy logs out of their digital caves",
		"Performing inception on nested log structures",
		"Bribing the firewall to let your logs through",
		"Recruiting an army of log-sniffing squirrels",
		"Deciphering cryptic server whispers",
		"Knitting a beautiful sweater from log threads",
		"Asking logs politely to organize themselves",
		"Deploying elite log commandos to retrieve data",
		"Massaging stubborn logs until they cooperate",
		"Untangling the web of log dependencies",
		"Negotiating peace treaties between conflicting logs",
		"Telepathically communicating with the log spirits",
		"Persuading logs to reveal their deepest secrets",
		"Training carrier pigeons to fetch remote logs",
		"Applying quantum mechanics to solve log paradoxes",
		"Hunting for the legendary golden log entry",
		"Excavating data from the archaeological log layers",
		"Following the cursors through the archive maze",
		"Deploying log fetching robots to all pages",
	}

	// Seed the random number generator
	randomSource := rand.NewSource(time.Now().UnixNano())
	randomGenerator := rand.New(randomSource)

	// Pick a random message
	return messages[randomGenerator.Intn(len(messages))]
}

// ReportError sends an error as a status message
func ReportError(ctx context.Context, statusCh chan<- Message, err error) ErrorMessage {
	if statusCh == nil {
		// No channel available, log to stderr
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return ErrorMessage{Error: err}
	}

	select {
	case statusCh <- Message{
		Status:         err.Error(),
		IsErrorMessage: true,
	}:
	default:
		// Do nothing
	}

	return ErrorMessage{Error: err}
}
