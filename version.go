package hsync

import (
	"time"
)

var (
	LibVersion        = "v0.3.2"
	LibReleaseDate, _ = time.Parse(time.RFC3339, "2026-08-11T11:00:00-07:00") // Usage: LibReleaseDate.Format(time.RFC3339)
)
