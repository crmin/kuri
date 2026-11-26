//go:build cgo

package main

import (
	"testing"
	"time"
)

func TestPopoverUptime(t *testing.T) {
	for _, test := range []struct {
		elapsed time.Duration
		want    string
	}{
		{-time.Hour, "0s"}, {10 * time.Second, "10s"}, {59 * time.Second, "59s"}, {time.Minute, "1m"},
		{3 * time.Minute, "3m"}, {59 * time.Minute, "59m"}, {time.Hour, "1h"}, {20 * time.Hour, "20h"},
		{24 * time.Hour, "1d"}, {5 * 24 * time.Hour, "5d"},
	} {
		if got := formatUptime(test.elapsed); got != test.want {
			t.Errorf("uptime for %v = %q, want %q", test.elapsed, got, test.want)
		}
	}
}
