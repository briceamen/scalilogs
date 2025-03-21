package tui

import (
	"fmt"
	"math/rand"
	"strings"
	"time"

	"github.com/briceamen/scalilogs/internal/status"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// AnimatedText represents a text that animates with dots
type AnimatedText struct {
	baseText    string
	currentText string
	lastUpdate  time.Time
	interval    time.Duration
	dotCount    int
	maxDots     int
}

// NewAnimatedText creates a new animated text with the given base text and animation interval
func NewAnimatedText(text string, interval time.Duration) AnimatedText {
	return AnimatedText{
		baseText:    text,
		currentText: text,
		lastUpdate:  time.Now(),
		interval:    interval,
		dotCount:    0,
		maxDots:     3,
	}
}

// UpdateAnimation updates the animated text by changing the number of dots
func (a *AnimatedText) UpdateAnimation() {
	if time.Since(a.lastUpdate) < a.interval {
		return
	}

	// Update dot count
	a.dotCount = (a.dotCount + 1) % (a.maxDots + 1)

	// Create the appropriate number of dots
	dots := strings.Repeat(".", a.dotCount)

	// Combine base text with dots
	a.currentText = a.baseText + dots

	a.lastUpdate = time.Now()
}

// GetText returns the current animated text
func (a *AnimatedText) GetText() string {
	return a.currentText
}

// TickAnimation is a tea.Cmd that ticks the animation
func TickAnimation() tea.Msg {
	return AnimationTickMsg{}
}

// AnimationTickMsg is a message to update animations
type AnimationTickMsg struct{}

var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#183BEE"))

	spinnerStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#183BEE"))

	stepStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#04B575"))

	errorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FF5B5B"))

	summaryHeaderStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("#183BEE"))

	SuccessStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#04B575"))
)

// ProgressEntry represents a line in the progress log
type ProgressEntry struct {
	Message string
	Value   int
}

// Model represents the Bubbletea state for the log extraction process
type Model struct {
	spinner           spinner.Model
	status            string
	appName           string
	targetTimestamp   string
	outputFile        string
	liveLogsCount     int
	archiveLogsCount  int
	totalLines        int
	filteredLineCount int
	archiveDetails    map[string]int
	elapsedTime       string
	isFinished        bool
	error             error
	// Store progress entries to display in View
	progressEntries []ProgressEntry
	// Summary content to display after finishing
	summary []string
	// Animated welcome text
	animatedText AnimatedText
}

// GetRandomWorkingMessage returns a random funny "we are working on it" message
func GetRandomWorkingMessage() string {
	messages := []string{
		"Feeding the hamster that powers the server",
		"Convincing the logs to reveal themselves",
		"Herding digital cats into orderly rows",
		"Untangling the spaghetti of your logs",
		"Brewing coffee for the overworked log parser",
		"Teaching the AI to read your logs faster",
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
	}

	// Seed the random number generator
	randomSource := rand.NewSource(time.Now().UnixNano())
	randomGenerator := rand.New(randomSource)

	// Pick a random message
	return messages[randomGenerator.Intn(len(messages))]
}

// NewModel creates a new Bubbletea model
func NewModel(appName, targetTimestamp string) Model {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = spinnerStyle

	// Create animated text with random message
	animatedText := NewAnimatedText(GetRandomWorkingMessage(), 250*time.Millisecond)

	return Model{
		spinner:         s,
		status:          "Starting...",
		appName:         appName,
		targetTimestamp: targetTimestamp,
		archiveDetails:  make(map[string]int),
		progressEntries: []ProgressEntry{},
		summary:         []string{},
		animatedText:    animatedText,
	}
}

// Init initializes the model
func (m Model) Init() tea.Cmd {
	return tea.Batch(
		m.spinner.Tick,
		TickAnimation,
	)
}

// Update handles updates to the model
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case AnimationTickMsg:
		m.animatedText.UpdateAnimation()
		return m, tea.Tick(50*time.Millisecond, func(t time.Time) tea.Msg {
			return AnimationTickMsg{}
		})

	case tea.KeyMsg:
		if msg.String() == "ctrl+c" || msg.String() == "q" && m.isFinished {
			return m, tea.Quit
		}
		return m, nil

	case status.Message:
		m.status = msg.Status

		// Store progress details for display in View
		if strings.Contains(msg.Status, "fetched") ||
			strings.Contains(msg.Status, "sorted") ||
			strings.Contains(msg.Status, "filtered") {

			entry := ProgressEntry{
				Message: msg.Status,
				Value:   msg.Value,
			}
			m.progressEntries = append(m.progressEntries, entry)

			switch {
			case strings.Contains(msg.Status, "live logs"):
				m.liveLogsCount = msg.Value
			case strings.Contains(msg.Status, "archive logs"):
				m.archiveLogsCount = msg.Value
			case strings.Contains(msg.Status, "total lines"):
				m.totalLines = msg.Value
			case strings.Contains(msg.Status, "filtered logs"):
				m.filteredLineCount = msg.Value
			}
		}

		return m, tea.Batch(
			m.spinner.Tick,
			TickAnimation,
		)

	case status.ErrorMessage:
		m.error = msg.Error
		return m, tea.Quit

	case status.FinishMessage:
		m.isFinished = true
		m.outputFile = msg.OutputFile
		m.liveLogsCount = msg.LiveLogsCount
		m.archiveLogsCount = msg.ArchiveLogsCount
		m.totalLines = msg.TotalLines
		m.filteredLineCount = msg.FilteredLineCount
		m.archiveDetails = msg.ArchiveDetails
		m.elapsedTime = msg.ElapsedTime

		// Create summary content for display in View
		m.summary = append(m.summary, summaryHeaderStyle.Render("--- Logs Fetch Summary ---"))
		m.summary = append(m.summary, fmt.Sprintf("- Live logs: %d lines", m.liveLogsCount))
		m.summary = append(m.summary, fmt.Sprintf("- Archive logs: %d lines", m.archiveLogsCount))

		if len(m.archiveDetails) > 0 {
			m.summary = append(m.summary, "  Archives:")
			for archiveTime, count := range m.archiveDetails {
				m.summary = append(m.summary, fmt.Sprintf("  - %s: %d lines", archiveTime, count))
			}
		}

		m.summary = append(m.summary, fmt.Sprintf("- Total combined logs: %d lines", m.totalLines))
		if m.filteredLineCount > 0 {
			m.summary = append(m.summary, fmt.Sprintf("- Final filtered logs: %d lines", m.filteredLineCount))
		}
		m.summary = append(m.summary, fmt.Sprintf("- Total processing time: %s", m.elapsedTime))
		m.summary = append(m.summary, "------------------------")

		return m, tea.Quit

	default:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, tea.Batch(
			cmd,
			TickAnimation,
		)
	}
}

// View renders the UI
func (m Model) View() string {
	if m.error != nil {
		return errorStyle.Render(fmt.Sprintf("Error: %v", m.error))
	}

	// If finished, return the full summary
	if m.isFinished {
		return strings.Join(m.summary, "\n")
	}

	// For ongoing operations, format progress
	var output strings.Builder

	// Display animated title instead of static title
	output.WriteString(titleStyle.Render(m.animatedText.GetText()) + "\n")

	// Display progress entries
	for _, entry := range m.progressEntries {
		// Only show the value if it's greater than zero
		if entry.Value > 0 {
			fmt.Fprintf(&output, "%s (%d lines)\n", stepStyle.Render(entry.Message), entry.Value)
		} else {
			fmt.Fprintf(&output, "%s\n", stepStyle.Render(entry.Message))
		}
	}

	// Add the current status with spinner
	spinner := m.spinner.View()
	if m.appName != "" {
		fmt.Fprintf(&output, "%s %s for %s", spinner, m.status, m.appName)
	} else {
		fmt.Fprintf(&output, "%s %s", spinner, m.status)
	}

	return output.String()
}
