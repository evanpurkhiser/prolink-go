package bpm

import (
	"time"
)

// ToDuration converts bpm and pitch information into a one beat duration.
func ToDuration(bpm, pitch float32) time.Duration {
	bps := ((pitch / 100 * bpm) + bpm) / 60

	return time.Duration(float32(time.Second) / bps)
}
