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
	"github.com/crmin/kuri/internal/settings"
	"golang.org/x/sys/unix"
)

const portChangePath = Directory + "/.port-change"

func ReadPortChange() (PortChange, error) {
	info, err := os.Lstat(portChangePath)
	if errors.Is(err, os.ErrNotExist) {
		return PortChange{}, nil
	}
	if err != nil {
		return PortChange{}, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0022 != 0 || info.Sys().(*syscall.Stat_t).Uid != 0 || info.Size() > 1024 {
		return PortChange{}, errors.New("Unsafe kuri port change record.")
	}
	data, err := os.ReadFile(portChangePath)
	if err != nil {
		return PortChange{}, err
	}
	boot, err := unix.Sysctl("kern.bootsessionuuid")
	if err != nil {
		return PortChange{}, err
	}
	return parsePortChange(string(data), boot, os.Geteuid())
}

func parsePortChange(record, boot string, uid int) (PortChange, error) {
	parts := strings.Split(strings.TrimSuffix(record, "\n"), "\n")
	if len(parts) != 4 {
		return PortChange{}, errors.New("Invalid kuri port change record.")
	}
	if parts[0] != boot || parts[1] != strconv.Itoa(uid) {
		return PortChange{}, nil
	}
	port, err := strconv.Atoi(parts[3])
	if err != nil || !settings.ValidPort(port) || parts[2] == "" {
		return PortChange{}, errors.New("Invalid kuri port change record.")
	}
	return PortChange{ID: parts[2], Port: port}, nil
}

func ChangePort(ctx context.Context, home string, change PortChange, stderr io.Writer) (bool, error) {
	account, err := startupAccount()
	if err != nil || account == "" {
		return false, errors.New("Could not determine the current kuri account.")
	}
	var output bytes.Buffer
	args := []string{"--configure-port", home, account, launchdassets.Plist, strconv.Itoa(change.Port), change.ID}
	if err := runStartupWithApproval(ctx, "change the port for", &output, stderr, func(admin bool) ([]byte, error) {
		return installerCommand(ctx, args, admin).CombinedOutput()
	}); err != nil {
		return false, err
	}
	switch strings.TrimSpace(output.String()) {
	case "loaded":
		return true, nil
	case "unloaded", "unchanged":
		return false, nil
	default:
		return false, fmt.Errorf("Unexpected kuri port change response: %s", output.String())
	}
}
