package credential

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
	kubeexec "k8s.io/client-go/plugin/pkg/client/auth/exec"
	clientconfig "k8s.io/client-go/tools/clientcmd/api"
)

const (
	Argument = "__exec-credential"
	Timeout  = 15 * time.Second
)

// Runner gives the native client-go authenticator a bounded command. The
// authenticator continues to own credential decoding, refresh, and caching.
type Runner struct {
	directory  string
	executable string
}

func New(runtimeDir string, retainExecutable bool) (*Runner, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	directory, err := os.MkdirTemp(runtimeDir, ".credential-")
	if err != nil {
		return nil, err
	}
	if retainExecutable {
		// go run can remove its executable as soon as the start command exits.
		path := filepath.Join(directory, "kuri")
		if err := retainBinary(executable, path); err != nil {
			os.RemoveAll(directory)
			return nil, err
		}
		executable = path
	}
	return &Runner{directory: directory, executable: executable}, nil
}

func retainBinary(source, destination string) error {
	if err := os.Link(source, destination); err == nil {
		return nil
	}
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0700)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	return errors.Join(copyErr, out.Close())
}

func (r *Runner) Close() error {
	return os.RemoveAll(r.directory)
}

func (r *Runner) Wrap(config *clientconfig.ExecConfig) error {
	if err := kubeexec.ValidatePluginPolicy(config.PluginPolicy); err != nil {
		return err
	}
	if config.PluginPolicy.PolicyType == clientconfig.PluginPolicyDenyAll {
		return nil // The native authenticator will reject execution.
	}
	original := filepath.Clean(config.Command)
	command, err := exec.LookPath(original)
	if err != nil {
		if config.InstallHint != "" {
			return fmt.Errorf("locate credential plugin: %w. %s", err, config.InstallHint)
		}
		return fmt.Errorf("locate credential plugin: %w", err)
	}
	if config.PluginPolicy.PolicyType == clientconfig.PluginPolicyAllowlist {
		if !allowed(original, command, config.PluginPolicy.Allowlist) {
			return errors.New("credential plugin is not permitted by the allowlist")
		}
		config.PluginPolicy.Allowlist = []clientconfig.AllowlistEntry{{Command: r.executable}}
	}
	command, err = filepath.Abs(command)
	if err != nil {
		return err
	}
	config.Command = r.executable
	config.Args = append([]string{Argument, r.directory, strconv.Itoa(os.Getpid()), command}, config.Args...)
	return nil
}

func allowed(original, command string, allowlist []clientconfig.AllowlistEntry) bool {
	for _, entry := range allowlist {
		if entry.Command == original || entry.Command == command {
			return true
		}
		if filepath.Base(entry.Command) != filepath.Base(command) {
			continue
		}
		if resolved, err := exec.LookPath(entry.Command); err == nil && resolved == command {
			return true
		}
	}
	return false
}

// Execute is the private CLI entry point used only by the native exec provider.
func Execute(args []string) error {
	if len(args) < 3 || !filepath.IsAbs(args[2]) {
		return errors.New("invalid credential helper invocation")
	}
	directory := args[0]
	parent, err := strconv.Atoi(args[1])
	if err != nil || !sessionAlive(directory, parent) {
		return errors.New("credential helper session has ended")
	}

	ctx, cancel := context.WithTimeout(context.Background(), Timeout)
	defer cancel()
	go func() {
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if !sessionAlive(directory, parent) {
					cancel()
					return
				}
			}
		}
	}()
	return run(ctx, args[2], args[3:], os.Stdin, os.Stdout, os.Stderr)
}

func sessionAlive(directory string, parent int) bool {
	if parent <= 1 || os.Getppid() != parent {
		return false
	}
	info, err := os.Stat(directory)
	return err == nil && info.IsDir()
}

func run(ctx context.Context, command string, args []string, stdin *os.File, stdout, stderr io.Writer) error {
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, stdout, stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if stdin != nil {
		fd := int(stdin.Fd())
		foreground, err := unix.IoctlGetInt(fd, unix.TIOCGPGRP)
		if err == nil && foreground == unix.Getpgrp() {
			signal.Ignore(syscall.SIGTTOU) // Allow the helper to restore the parent group.
			cmd.SysProcAttr.Foreground, cmd.SysProcAttr.Ctty = true, fd
			defer unix.IoctlSetPointerInt(fd, unix.TIOCSPGRP, foreground)
		}
	}
	cmd.Cancel = func() error {
		err := unix.Kill(-cmd.Process.Pid, unix.SIGKILL)
		if errors.Is(err, unix.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	err := cmd.Run()
	if ctx.Err() != nil {
		return fmt.Errorf("credential plugin stopped: %w", ctx.Err())
	}
	return err
}
