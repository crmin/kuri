//go:build darwin && cgo

package launchd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"syscall"

	launchdassets "github.com/crmin/kuri/deploy/launchd"
	"golang.org/x/sys/unix"
)

const menuPausePath = "/Library/Application Support/kuri/.menu-pause"

func LockLifecycle(ctx context.Context, home string) (io.Closer, error) {
	return lockUserOperation(ctx, home, "lifecycle.lock")
}

func MenuPaused() (bool, error) {
	info, err := os.Lstat(menuPausePath)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0022 != 0 || info.Sys().(*syscall.Stat_t).Uid != 0 || info.Size() > 4096 {
		return false, errors.New("Unsafe kuri pause record.")
	}
	data, err := os.ReadFile(menuPausePath)
	if err != nil {
		return false, err
	}
	boot, err := unix.Sysctl("kern.bootsessionuuid")
	if err != nil || boot == "" {
		return false, errors.New("Could not identify the current boot session.")
	}
	return currentMenuPause(string(data), boot, os.Geteuid())
}

func currentMenuPause(record, boot string, uid int) (bool, error) {
	lines := strings.Split(strings.TrimSuffix(record, "\n"), "\n")
	if len(lines) != 4 {
		return false, errors.New("Invalid kuri pause record.")
	}
	if lines[0] != boot {
		return false, nil
	}
	if lines[1] != strconv.Itoa(uid) {
		return false, errors.New("The paused kuri registration belongs to a different macOS account.")
	}
	switch lines[3] {
	case "pausing", "paused", "resuming":
		return true, nil
	default:
		return false, errors.New("Invalid kuri pause phase.")
	}
}

func PauseMenu(ctx context.Context, stderr io.Writer) (int, error) {
	var out bytes.Buffer
	if err := menuRegistration(ctx, "pause", &out, stderr); err != nil {
		return 0, err
	}
	for line := range strings.SplitSeq(out.String(), "\n") {
		if value, ok := strings.CutPrefix(line, "pid="); ok {
			pid, err := strconv.Atoi(value)
			if err != nil || pid <= 0 {
				return 0, errors.New("Invalid kuri LaunchDaemon process ID.")
			}
			return pid, nil
		}
	}
	return 0, nil
}

func ResumeMenu(ctx context.Context, stderr io.Writer) error {
	return menuRegistration(ctx, "resume", io.Discard, stderr)
}

func FinishMenuResume(ctx context.Context, stderr io.Writer) error {
	return menuRegistration(ctx, "finish-resume", io.Discard, stderr)
}

func menuRegistration(ctx context.Context, operation string, stdout, stderr io.Writer) error {
	account, err := startupAccount()
	if err != nil || account == "" {
		return fmt.Errorf("could not determine the current kuri account")
	}
	args := []string{"--menu-" + operation, "", account, launchdassets.Plist}
	return runStartupWithApproval(ctx, operation, stdout, stderr, func(admin bool) ([]byte, error) {
		return installerCommand(ctx, args, admin).CombinedOutput()
	})
}
