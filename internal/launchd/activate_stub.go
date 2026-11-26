//go:build !darwin || !cgo

package launchd

import "errors"

func activateDescriptors(string) ([]int, error) {
	return nil, errors.New("launchd socket activation requires macOS and a cgo build")
}
