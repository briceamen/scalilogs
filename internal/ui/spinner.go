package ui

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// Spinner represents a simple CLI spinner
type Spinner struct {
	frames   []string
	message  string
	stopChan chan struct{}
	doneChan chan struct{}
	active   bool
	mu       sync.Mutex
}

// NewSpinner creates a new spinner with the given message
func NewSpinner(message string) *Spinner {
	return &Spinner{
		frames:   []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"},
		message:  message,
		stopChan: make(chan struct{}),
		doneChan: make(chan struct{}),
	}
}

// Start begins the spinner animation
func (s *Spinner) Start() {
	s.mu.Lock()
	if s.active {
		s.mu.Unlock()
		return
	}
	s.active = true
	s.mu.Unlock()

	go func() {
		defer close(s.doneChan)
		frame := 0
		for {
			select {
			case <-s.stopChan:
				s.clear()
				return
			default:
				s.mu.Lock()
				frameChar := s.frames[frame]
				message := s.message
				s.mu.Unlock()

				fmt.Printf("\r%s %s", frameChar, message)
				frame = (frame + 1) % len(s.frames)
				time.Sleep(100 * time.Millisecond)
			}
		}
	}()
}

// Stop halts the spinner animation
func (s *Spinner) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.active {
		return
	}

	close(s.stopChan)
	<-s.doneChan
	s.active = false

	// Create new channels for next start
	s.stopChan = make(chan struct{})
	s.doneChan = make(chan struct{})
}

// Update changes the spinner message
func (s *Spinner) Update(message string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Clear the current line first
	s.clear()

	// Update the message, ensuring it has no newlines that could break formatting
	s.message = strings.ReplaceAll(message, "\n", " ")
}

// clear clears the current line in the terminal
func (s *Spinner) clear() {
	fmt.Print("\r")
	fmt.Print(strings.Repeat(" ", len(s.message)+2))
	fmt.Print("\r")
}
