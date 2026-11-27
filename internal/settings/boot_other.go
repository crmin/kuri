//go:build !darwin

package settings

func bootSession() (string, error) { return "", nil }
