package tui

import (
	"fmt"
	"strings"

	"github.com/briceamen/scalilogs/internal/status"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#7D56F4"))

	spinnerStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#7D56F4"))

	stepStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#04B575"))

	errorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FF0000"))

	logCountStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#05B2DC"))

	summaryHeaderStyle = lipgloss.NewStyle().
				Bold(true).
				Underline(true).
				Foreground(lipgloss.Color("#04B575"))

	summaryContentStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#FFFFFF"))
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
}

// NewModel creates a new Bubbletea model
func NewModel(appName, targetTimestamp string) Model {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = spinnerStyle

	return Model{
		spinner:         s,
		status:          "Starting...",
		appName:         appName,
		targetTimestamp: targetTimestamp,
		archiveDetails:  make(map[string]int),
		progressEntries: []ProgressEntry{},
		summary:         []string{},
	}
}

// Init initializes the model
func (m Model) Init() tea.Cmd {
	return m.spinner.Tick
}

// Update handles updates to the model
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" || msg.String() == "q" && m.isFinished {
			return m, tea.Quit
		}
		return m, nil

	case status.StatusMessage:
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

		return m, m.spinner.Tick

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
		m.summary = append(m.summary, stepStyle.Render("✓ ")+"Extraction complete!")
		m.summary = append(m.summary, fmt.Sprintf("Logs saved to: %s", m.outputFile))
		m.summary = append(m.summary, "")
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
		return m, cmd
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
