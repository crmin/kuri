package launchd

// Registration describes the managed job on disk, including jobs paused
// for the current boot and jobs registered only for socket activation.
type Registration struct {
	Program string
	Port    int
	AtBoot  bool
}

func (r Registration) Present() bool { return r.Program != "" }
