package credential

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	clientconfig "k8s.io/client-go/tools/clientcmd/api"
)

func TestCredentialRunnerPreservesStreams(t *testing.T) {
	t.Setenv("KUBERNETES_EXEC_INFO", "credential request")
	var out, stderr bytes.Buffer
	if err := run(t.Context(), "/bin/sh",
		[]string{"-c", "printf '%s:%s' \"$KUBERNETES_EXEC_INFO\" \"$1\"; printf 'diagnostic' >&2", "plugin", "argument"},
		nil, &out, &stderr); err != nil {
		t.Fatal(err)
	}
	if out.String() != "credential request:argument" || stderr.String() != "diagnostic" {
		t.Fatalf("unexpected plugin streams: %q, %q", out.String(), stderr.String())
	}
}

func TestCredentialCancellationTerminatesProcessGroup(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	path := filepath.Join(t.TempDir(), "child.pid")
	done := make(chan error, 1)
	go func() {
		done <- run(ctx, "/bin/sh",
			[]string{"-c", "sleep 30 & echo $! > \"$1\"; wait", "plugin", path}, nil, io.Discard, io.Discard)
	}()
	deadline := time.Now().Add(3 * time.Second)
	var pid string
	for time.Now().Before(deadline) {
		line, _ := os.ReadFile(path)
		pid = strings.TrimSpace(string(line))
		if _, err := strconv.Atoi(pid); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := strconv.Atoi(pid); err != nil {
		t.Fatal("credential plugin did not start its child")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancel returned %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("credential command survived cancellation")
	}
	for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); {
		status, _ := exec.Command("/bin/ps", "-p", pid, "-o", "stat=").Output()
		if state := strings.TrimSpace(string(status)); state == "" || strings.HasPrefix(state, "Z") {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("child survived credential cancellation")
}

func TestWrapPreservesCommandPolicy(t *testing.T) {
	runner, err := New(t.TempDir(), true)
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close()
	config := &clientconfig.ExecConfig{Command: "/bin/sh", PluginPolicy: clientconfig.PluginPolicy{
		PolicyType: clientconfig.PluginPolicyAllowlist, Allowlist: []clientconfig.AllowlistEntry{{Command: "/bin/false"}},
	}}
	if err := runner.Wrap(config); err == nil {
		t.Fatal("credential helper bypassed the allowlist")
	}
	config.PluginPolicy.Allowlist[0].Command = "/bin/sh"
	if err := runner.Wrap(config); err != nil {
		t.Fatal(err)
	}
	if config.Command != runner.executable || config.Args[3] != "/bin/sh" ||
		config.PluginPolicy.Allowlist[0].Command != runner.executable {
		t.Fatal("permitted credential command was not wrapped safely")
	}
}
