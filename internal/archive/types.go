package archive

import (
	"time"
)

// ArchiveInfo stores information about a log archive
type ArchiveInfo struct {
	URL       string
	FromTime  time.Time
	ToTime    time.Time
	Size      string
	Index     int
	ArchiveID string
}
