//go:build cgo

package main

import (
	"fmt"
	"time"
)

func formatUptime(elapsed time.Duration) string {
	elapsed = max(elapsed, 0)
	const day = 24 * time.Hour
	switch {
	case elapsed >= day:
		return fmt.Sprintf("%dd", elapsed/day)
	case elapsed >= time.Hour:
		return fmt.Sprintf("%dh", elapsed/time.Hour)
	case elapsed >= time.Minute:
		return fmt.Sprintf("%dm", elapsed/time.Minute)
	default:
		return fmt.Sprintf("%ds", elapsed/time.Second)
	}
}
