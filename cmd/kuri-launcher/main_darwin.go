package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
)

func main() {
	if err := start(); err != nil {
		fmt.Fprintf(os.Stderr, "Could not start kuri: %v\n", err)
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() > 0 {
			os.Exit(exit.ExitCode())
		}
		os.Exit(1)
	}
}

func start() error {
	launcher, err := os.Executable()
	if err != nil {
		return err
	}
	binary := filepath.Join(filepath.Dir(launcher), "kuri")

	// Finder may omit the directories where kubeconfig exec plugins are installed.
	path := strings.Split(os.Getenv("PATH"), string(os.PathListSeparator))
	path = slices.DeleteFunc(path, func(dir string) bool { return dir == "" })
	for _, dir := range []string{"/opt/homebrew/bin", "/usr/local/bin", "/usr/bin", "/bin", "/usr/sbin", "/sbin"} {
		if !slices.Contains(path, dir) {
			path = append(path, dir)
		}
	}
	env := slices.DeleteFunc(os.Environ(), func(entry string) bool {
		return strings.HasPrefix(entry, "PATH=") || strings.HasPrefix(entry, "KURI_LAUNCHD_START=")
	})
	env = append(env, "PATH="+strings.Join(path, string(os.PathListSeparator)))
	if stderrIsDiscarded() {
		return startWithFailureLog(binary, env)
	}
	return syscall.Exec(binary, []string{binary, "start"}, env)
}
