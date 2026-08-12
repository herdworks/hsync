package hsync

import (
	"time"
)

var (
	LibName           = "hsync"
	LibVersion        = "v0.4.0"
	LibReleaseDate, _ = time.Parse(time.RFC3339, "2026-08-11T11:00:00-07:00") // Usage: LibReleaseDate.Format(time.RFC3339)
)
