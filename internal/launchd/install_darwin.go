//go:build darwin && cgo

package launchd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	launchdassets "github.com/crmin/kuri/deploy/launchd"
	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

const serviceTarget = "system/com.github.crmin.kuri"

const administratorScript = `on run args
	set shellCommand to "/bin/sh -c"
	repeat with argument in args
		set shellCommand to shellCommand & " " & quoted form of (argument as text)
	end repeat
	with timeout of 180 seconds
		do shell script shellCommand with administrator privileges
	end timeout
end run`

func Supported() bool { return true }

func InstallStartup(ctx context.Context, stdout, stderr io.Writer) error {
	directory, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("find the current directory: %w", err)
	}
	return changeStartup(ctx, "install", filepath.Join(directory, "kuri"), stdout, stderr)
}

func UninstallStartup(ctx context.Context, stdout, stderr io.Writer) error {
	return changeStartup(ctx, "uninstall", "", stdout, stderr)
}

func changeStartup(ctx context.Context, operation, source string, stdout, stderr io.Writer) error {
	account, err := startupAccount()
	if err != nil {
		return err
	}
	args := []string{"--startup-" + operation, source, account, launchdassets.Plist}
	return runStartupWithApproval(ctx, operation, stdout, stderr, func(admin bool) ([]byte, error) {
		return installerCommand(ctx, args, admin).CombinedOutput()
	})
}

func startupAccount() (string, error) {
	uid := strconv.Itoa(os.Geteuid())
	if uid == "0" {
		// The script can use the existing registration or console account when
		// the command was started by root outside sudo.
		uid = os.Getenv("SUDO_UID")
		if uid == "" || uid == "0" {
			return "", nil
		}
	}
	account, err := user.LookupId(uid)
	if err != nil {
		return "", fmt.Errorf("look up the requesting macOS account: %w", err)
	}
	return account.Username, nil
}

func runStartupWithApproval(ctx context.Context, operation string, stdout, stderr io.Writer, run func(admin bool) ([]byte, error)) error {
	out, err := run(false)
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 77 && ctx.Err() == nil && os.Geteuid() != 0 {
		fmt.Fprintf(stderr, "kuri needs administrator approval to %s its startup registration.\n", operation)
		out, err = run(true)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err != nil {
		if reason := strings.TrimSpace(string(out)); reason != "" {
			return fmt.Errorf("%s kuri startup: %s", operation, reason)
		}
		return fmt.Errorf("%s kuri startup: %w", operation, err)
	}
	_, err = stdout.Write(out)
	return err
}

func installerCommand(ctx context.Context, args []string, admin bool) *exec.Cmd {
	args = append([]string{launchdassets.InstallScript, "kuri-install"}, args...)
	var cmd *exec.Cmd
	switch {
	case !admin || os.Geteuid() == 0:
		cmd = exec.CommandContext(ctx, "/bin/sh", append([]string{"-c"}, args...)...)
	case term.IsTerminal(int(os.Stdin.Fd())):
		cmd = exec.CommandContext(ctx, "/usr/bin/sudo", append([]string{"/bin/sh", "-c"}, args...)...)
		cmd.Stdin = os.Stdin
	default:
		cmd = exec.CommandContext(ctx, "/usr/bin/osascript", append([]string{"-e", administratorScript}, args...)...)
	}
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = 15 * time.Second
	return cmd
}

// Register installs the current executable for the requesting account. The
// privileged script creates sockets, which launchd then hands to that user.
func Register(ctx context.Context, home, activation string) error {
	lock, err := lockRegistration(ctx, home)
	if err != nil {
		return err
	}
	defer lock.Close()

	// Another invocation may have completed installation while we waited.
	if exec.CommandContext(ctx, "/bin/launchctl", "print", serviceTarget).Run() == nil {
		info, err := os.Lstat(activation)
		if err == nil && info.Mode()&os.ModeSocket != 0 &&
			info.Mode().Perm()&0077 == 0 && int(info.Sys().(*syscall.Stat_t).Uid) == os.Geteuid() {
			return nil
		}
		return errors.New("kuri is already registered, but its activation socket is unavailable to this macOS account")
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	account, err := user.LookupId(strconv.Itoa(os.Geteuid()))
	if err != nil {
		return fmt.Errorf("look up the current macOS account: %w", err)
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	args := []string{"--first-run", executable, account.Username, home, launchdassets.Plist}
	if os.Geteuid() != 0 {
		fmt.Fprintln(os.Stderr, "kuri needs administrator approval to register its LaunchDaemon for loopback port 80.")
	}
	out, err := installerCommand(ctx, args, true).CombinedOutput()
	if err == nil {
		return nil
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if reason := strings.TrimSpace(string(out)); reason != "" {
		return fmt.Errorf("register the kuri LaunchDaemon: %s", reason)
	}
	return fmt.Errorf("register the kuri LaunchDaemon: %w", err)
}

func lockRegistration(ctx context.Context, home string) (*os.File, error) {
	dir := filepath.Join(home, ".kuri")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("create kuri runtime directory: %w", err)
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode().Perm()&0022 != 0 || int(info.Sys().(*syscall.Stat_t).Uid) != os.Geteuid() {
		return nil, errors.New("kuri runtime directory must be owned by the current macOS account and protected from writes by other users")
	}
	path := filepath.Join(dir, "install.lock")
	fd, err := unix.Open(path, unix.O_WRONLY|unix.O_CREAT|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return nil, fmt.Errorf("open LaunchDaemon registration lock: %w", err)
	}
	file := os.NewFile(uintptr(fd), path)
	retry := time.NewTicker(50 * time.Millisecond)
	defer retry.Stop()
	for ctx.Err() == nil {
		if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err == nil {
			return file, nil
		} else if !errors.Is(err, unix.EWOULDBLOCK) {
			file.Close()
			return nil, fmt.Errorf("lock LaunchDaemon registration: %w", err)
		}
		select {
		case <-ctx.Done():
		case <-retry.C:
		}
	}
	file.Close()
	return nil, ctx.Err()
}
