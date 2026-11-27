package settings

import (
	"errors"

	"golang.org/x/sys/unix"
)

func bootSession() (string, error) {
	boot, err := unix.Sysctl("kern.bootsessionuuid")
	if err == nil && boot == "" {
		err = errors.New("Could not identify the current macOS boot session.")
	}
	return boot, err
}
