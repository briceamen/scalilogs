package tui

import (
	"fmt"
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
// and occasionally changing the message entirely
func (a *AnimatedText) UpdateAnimation() {
	if time.Since(a.lastUpdate) < a.interval {
		return
	}

	// Update dot count
	a.dotCount = (a.dotCount + 1) % (a.maxDots + 1)

	// Determine which text to use as base
	textBase := a.baseText

	// Create the appropriate number of dots
	dots := strings.Repeat(".", a.dotCount)

	// Combine base text with dots
	a.currentText = textBase + dots

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
	// Summary content to display after finishing
	summary []string
	// Animated welcome text
	animatedText AnimatedText
	// Store the full finish message for detailed reporting
	msg status.FinishMessage
	// Track the last status update time
	lastStatusUpdate time.Time
}

// NewModel creates a new Bubbletea model
func NewModel(appName, targetTimestamp string) Model {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = spinnerStyle

	// Create animated text with random message
	// Dot animation plays every 250ms, but we'll use 2 seconds for message changes
	animatedText := NewAnimatedText(status.GetRandomWorkingMessage(), 250*time.Millisecond)

	return Model{
		spinner:          s,
		status:           "Starting...",
		appName:          appName,
		targetTimestamp:  targetTimestamp,
		archiveDetails:   make(map[string]int),
		summary:          []string{},
		animatedText:     animatedText,
		lastStatusUpdate: time.Now(),
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
		// Always allow quitting with ctrl+c
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}

		// Allow quitting with q once finished or if there's an error
		if msg.String() == "q" && (m.isFinished || m.error != nil) {
			return m, tea.Quit
		}
		return m, nil

	case status.Message:
		// Update status fields based on message
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

		// Update animated text with status message
		m.status = msg.Status

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
		// Store the complete message for detailed reporting
		m.msg = msg

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

		// Add detailed timing information in chronological order of execution
		m.summary = append(m.summary, "  Processing breakdown:")
		if m.msg.ArchiveSelectionTime != "" {
			m.summary = append(m.summary, fmt.Sprintf("  - Archive selection time: %s", m.msg.ArchiveSelectionTime))
		}
		if m.msg.FetchArchiveTime != "" {
			m.summary = append(m.summary, fmt.Sprintf("  - Archive logs fetch time: %s", m.msg.FetchArchiveTime))
		}
		if m.msg.FetchLiveTime != "" {
			m.summary = append(m.summary, fmt.Sprintf("  - Live logs fetch time: %s", m.msg.FetchLiveTime))
		}
		if m.msg.SortTime != "" {
			m.summary = append(m.summary, fmt.Sprintf("  - Sort time: %s", m.msg.SortTime))
		}
		if m.msg.FilterTime != "" {
			m.summary = append(m.summary, fmt.Sprintf("  - Filter time: %s", m.msg.FilterTime))
		}

		// Use the same style for bottom separator as the header
		m.summary = append(m.summary, summaryHeaderStyle.Render("------------------------"))

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
		// Clean error message for display - strip duplicate info
		errMsg := m.error.Error()

		return errorStyle.Render(fmt.Sprintf("Error: %s\n\nPress q to exit", errMsg))
	}

	// If finished, return the full summary
	if m.isFinished {
		// If outputFile is empty, this suggests there was an error but it wasn't properly captured
		if m.outputFile == "" {
			return errorStyle.Render("Error: Log extraction failed, but no specific error was captured. Check your client configuration or authentication.\n\nPress q to exit")
		}
		return strings.Join(m.summary, "\n") + "\n\nPress q to exit"
	}

	// For ongoing operations, format progress
	var output strings.Builder

	// Display animated title
	output.WriteString(titleStyle.Render(m.animatedText.GetText()) + "\n")

	// Add the current status with spinner
	spinner := m.spinner.View()
	if m.appName != "" {
		fmt.Fprintf(&output, "%s %s for %s", spinner, m.status, m.appName)
	} else {
		fmt.Fprintf(&output, "%s %s", spinner, m.status)
	}

	return output.String()
}
