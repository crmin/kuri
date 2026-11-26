//go:build darwin && cgo

package launchd

import (
	"context"
	"errors"
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

func ReadMenuPause() (MenuPause, error) {
	info, err := os.Lstat(menuPausePath)
	if errors.Is(err, os.ErrNotExist) {
		return MenuPause{}, nil
	}
	if err != nil {
		return MenuPause{}, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0022 != 0 || info.Sys().(*syscall.Stat_t).Uid != 0 || info.Size() > 4096 {
		return MenuPause{}, errors.New("Unsafe kuri pause record.")
	}
	data, err := os.ReadFile(menuPausePath)
	if err != nil {
		return MenuPause{}, err
	}
	boot, err := unix.Sysctl("kern.bootsessionuuid")
	if err != nil || boot == "" {
		return MenuPause{}, errors.New("Could not identify the current boot session.")
	}
	return parseMenuPause(string(data), boot, os.Geteuid())
}

func currentMenuPause(record, boot string, uid int) (bool, error) {
	pause, err := parseMenuPause(record, boot, uid)
	return pause.Phase != "", err
}

func parseMenuPause(record, boot string, uid int) (MenuPause, error) {
	lines := strings.Split(strings.TrimSuffix(record, "\n"), "\n")
	if len(lines) != 4 && len(lines) != 7 {
		return MenuPause{}, errors.New("Invalid kuri pause record.")
	}
	if lines[0] != boot || lines[3] == "completed" {
		return MenuPause{}, nil
	}
	if lines[1] != strconv.Itoa(uid) {
		return MenuPause{}, errors.New("The paused kuri registration belongs to a different macOS account.")
	}
	switch lines[3] {
	case "pausing", "paused", "resuming":
		pause := MenuPause{Phase: lines[3]}
		if len(lines) == 7 {
			pause.Operation = lines[4]
		}
		return pause, nil
	default:
		return MenuPause{}, errors.New("Invalid kuri pause phase.")
	}
}

func ChangeMenuRegistration(ctx context.Context, action, home, operation, expected string, stderr io.Writer) error {
	account, err := startupAccount()
	if err != nil || account == "" {
		return errors.New("Could not determine the current kuri account.")
	}
	args := []string{"--menu-" + action, home, account, launchdassets.Plist, operation, expected}
	return runStartupWithApproval(ctx, action, io.Discard, stderr, func(admin bool) ([]byte, error) {
		return installerCommand(ctx, args, admin).CombinedOutput()
	})
}
