package hsync

import (
	"time"
)

var (
	LibName           = "hsync"
	LibVersion        = "v0.7.4"
	LibReleaseDate, _ = time.Parse(time.RFC3339, "2026-10-05T08:00:00-07:00") // Usage: LibReleaseDate.Format(time.RFC3339)
)
