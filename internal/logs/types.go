package logs

import (
	"time"
)

// LogLine represents a single log line with its timestamp
type LogLine struct {
	Timestamp time.Time
	Content   string
}
