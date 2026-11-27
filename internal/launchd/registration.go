package launchd

import "errors"

var ErrOtherRegistration = errors.New("The kuri LaunchDaemon belongs to a different macOS account or home directory.")

const managedProgram = Directory + "/kuri"

// Registration describes the managed job on disk, including jobs paused
// for the current boot and jobs registered only for socket activation.
type Registration struct {
	Program    string
	Port       int
	AtBoot     bool
	AppStartup bool
}

func (r Registration) Present() bool { return r.Program != "" }

func (r Registration) CanUpdateFromApp() bool { return r.AppStartup || r.Program == managedProgram }

func (r Registration) ProxyUpdateError() error {
	if r.CanUpdateFromApp() {
		return errors.New("Update Proxy Registration in kuri Settings before stopping the proxy.")
	}
	return errors.New("Build the latest kuri CLI and run ./kuri install from its directory to update the proxy registration.")
}
