//go:build darwin && cgo

package launchd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	launchdassets "github.com/crmin/kuri/deploy/launchd"
)

const (
	registrationPath = "/Library/LaunchDaemons/com.github.crmin.kuri.plist"
	managedProgram   = Directory + "/kuri"
	startupLink      = "/usr/local/bin/kuri"
)

func Registered(ctx context.Context, home string) (Registration, error) {
	info, err := os.Lstat(registrationPath)
	if errors.Is(err, os.ErrNotExist) {
		return Registration{}, nil
	}
	if err != nil {
		return Registration{}, err
	}
	if !info.Mode().IsRegular() || info.Sys().(*syscall.Stat_t).Uid != 0 ||
		info.Mode().Perm()&0022 != 0 || info.Size() > 64<<10 {
		return Registration{}, errors.New("Unsafe kuri LaunchDaemon registration.")
	}
	account, err := startupAccount()
	if err != nil {
		return Registration{}, err
	}
	raw, err := exec.CommandContext(ctx, "/usr/bin/plutil", "-convert", "json", "-o", "-", registrationPath).Output()
	if err != nil {
		return Registration{}, fmt.Errorf("read kuri LaunchDaemon registration: %w", err)
	}
	return decodeRegistration(raw, account, home)
}

func decodeRegistration(raw []byte, account, home string) (Registration, error) {
	var job struct {
		Label, UserName      string
		ProgramArguments     []string
		EnvironmentVariables struct {
			Home        string `json:"HOME"`
			StartMarker string `json:"KURI_LAUNCHD_START"`
		}
		RunAtLoad bool
		Sockets   struct {
			HTTP []struct {
				Port   int    `json:"SockServiceName"`
				Family string `json:"SockFamily"`
				Node   string `json:"SockNodeName"`
			}
		}
	}
	if err := json.Unmarshal(raw, &job); err != nil {
		return Registration{}, fmt.Errorf("read kuri LaunchDaemon registration: %w", err)
	}
	if account == "" || job.UserName != account || filepath.Clean(job.EnvironmentVariables.Home) != filepath.Clean(home) {
		return Registration{}, errors.New("The kuri LaunchDaemon belongs to a different macOS account or home directory.")
	}
	args := job.ProgramArguments
	http := job.Sockets.HTTP
	if job.Label != "com.github.crmin.kuri" || len(args) < 2 || len(http) != 2 ||
		http[0].Port < 1 || http[0].Port > 65535 || http[0].Port != http[1].Port ||
		http[0].Family != "IPv4" || http[0].Node != "127.0.0.1" ||
		http[1].Family != "IPv6" || http[1].Node != "::1" {
		return Registration{}, errors.New("Unrecognized kuri LaunchDaemon registration.")
	}
	switch {
	case len(args) == 2 && args[0] == startupLink && args[1] == "start" &&
		job.RunAtLoad && job.EnvironmentVariables.StartMarker == "1":
	case args[0] == managedProgram && args[1] == "__launchd" && job.EnvironmentVariables.StartMarker == "" &&
		((len(args) == 2 && !job.RunAtLoad) || (len(args) == 3 && job.RunAtLoad && args[2] == "--eager")):
	default:
		return Registration{}, errors.New("Unrecognized kuri LaunchDaemon registration.")
	}
	return Registration{Program: args[0], Port: http[0].Port, AtBoot: job.RunAtLoad}, nil
}

func (r Registration) SupportsSettings(ctx context.Context) bool {
	if !r.Present() {
		return true
	}
	if os.Geteuid() == 0 {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	answer, err := exec.CommandContext(ctx, r.Program, "__settings_version").Output()
	return err == nil && strings.TrimSpace(string(answer)) == "1"
}

func EnsurePolicySupport(ctx context.Context, home string, stderr io.Writer) error {
	registration, err := Registered(ctx, home)
	if err != nil {
		return err
	}
	if registration.SupportsSettings(ctx) {
		return nil
	}
	if registration.Program != managedProgram {
		return errors.New("Update the installed kuri binary to apply the saved settings.")
	}
	account, err := startupAccount()
	if err != nil {
		return err
	}
	binary, err := os.Executable()
	if err != nil {
		return err
	}
	args := []string{"--upgrade-policy", binary, account, home, launchdassets.Plist}
	if err := runStartupWithApproval(ctx, "update", io.Discard, stderr, func(admin bool) ([]byte, error) {
		return installerCommand(ctx, args, admin).CombinedOutput()
	}); err != nil {
		return err
	}
	if !registration.SupportsSettings(ctx) {
		return errors.New("Update the installed kuri binary to apply the saved settings.")
	}
	return nil
}
