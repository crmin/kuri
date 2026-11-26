//go:build darwin && cgo

package launchd

import (
	"context"
	"errors"
	"fmt"
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
	args := []string{launchdassets.InstallScript, "kuri-install", "--first-run", executable, account.Username, home, launchdassets.Plist}
	var cmd *exec.Cmd
	if term.IsTerminal(int(os.Stdin.Fd())) {
		cmd = exec.CommandContext(ctx, "/usr/bin/sudo", append([]string{"/bin/sh", "-c"}, args...)...)
		cmd.Stdin = os.Stdin
	} else {
		cmd = exec.CommandContext(ctx, "/usr/bin/osascript", append([]string{"-e", administratorScript}, args...)...)
	}
	fmt.Fprintln(os.Stderr, "kuri needs administrator approval to register its LaunchDaemon for loopback port 80.")
	out, err := cmd.CombinedOutput()
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
