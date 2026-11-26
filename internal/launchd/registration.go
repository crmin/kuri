package launchd

import "errors"

var ErrOtherRegistration = errors.New("The kuri LaunchDaemon belongs to a different macOS account or home directory.")

// Registration describes the managed job on disk, including jobs paused
// for the current boot and jobs registered only for socket activation.
type Registration struct {
	Program string
	Port    int
	AtBoot  bool
}

func (r Registration) Present() bool { return r.Program != "" }
