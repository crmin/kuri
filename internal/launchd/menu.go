package launchd

type MenuPause struct {
	Phase     string
	Operation string
}

func MenuPaused() (bool, error) {
	pause, err := ReadMenuPause()
	return pause.Phase != "", err
}
