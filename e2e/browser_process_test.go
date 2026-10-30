//go:build browser

package e2e_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// runBrowserScript keeps Bun and its browser descendants in one process group.
// Context cancellation can then stop the whole browser tree, and WaitDelay
// bounds any output pipe kept open by a child that outlives Bun.
func runBrowserScript(ctx context.Context, dir string, env []string, scriptPath string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "bun", scriptPath)
	cmd.Dir = dir
	cmd.Env = localBrowserEnv(env)
	return runBrowserCommand(ctx, cmd)
}

func localBrowserEnv(env []string) []string {
	filtered := make([]string, 0, len(env))
	for _, entry := range env {
		key, _, _ := strings.Cut(entry, "=")
		switch strings.ToLower(key) {
		case "http_proxy", "https_proxy", "all_proxy":
			continue
		default:
			filtered = append(filtered, entry)
		}
	}
	return filtered
}

func runBrowserCommand(ctx context.Context, cmd *exec.Cmd) ([]byte, error) {
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = 3 * time.Second

	output, runErr := cmd.CombinedOutput()
	if cmd.Process != nil {
		// The process group is unique to this invocation. Clean up only its
		// descendants, including Chromium if Bun exited before it did. Give
		// children a brief grace period to exit on SIGTERM, then force cleanup.
		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM); err == nil {
			time.Sleep(150 * time.Millisecond)
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
	}
	if ctx.Err() != nil {
		return output, fmt.Errorf("browser script context ended: %w (command error: %v)", ctx.Err(), runErr)
	}
	return output, runErr
}

func TestBrowserProcessGroupClosesInheritedOutputPipe(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", "-c", "(printf child-output; sleep 30) & exit 0")
	started := time.Now()
	output, err := runBrowserCommand(ctx, cmd)
	elapsed := time.Since(started)
	if !errors.Is(err, exec.ErrWaitDelay) && !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected bounded timeout/pipe wait, got %v; output=%q", err, output)
	}
	if !strings.Contains(string(output), "child-output") {
		t.Fatalf("did not capture child output: %q", output)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("process group cleanup took too long: %s", elapsed)
	}
	if cmd.Process == nil {
		t.Fatal("command did not start")
	}
	pgid := strconv.Itoa(cmd.Process.Pid)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if !hasLiveProcessGroup(t, pgid) {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("process group %s still has a live member after cleanup", pgid)
}

func TestLocalBrowserEnvDropsOnlyProxySettings(t *testing.T) {
	input := []string{"PATH=/bin", "HTTP_PROXY=http://proxy.invalid", "http_proxy=http://proxy.invalid", "no_proxy=127.0.0.1", "E2E_WEB=http://127.0.0.1:3000"}
	got := localBrowserEnv(input)
	want := []string{"PATH=/bin", "no_proxy=127.0.0.1", "E2E_WEB=http://127.0.0.1:3000"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("local browser environment = %#v, want %#v", got, want)
	}
}

func hasLiveProcessGroup(t *testing.T, pgid string) bool {
	t.Helper()
	output, err := exec.Command("ps", "-eo", "pgid=,stat=").Output()
	if err != nil {
		t.Fatalf("list processes: %v", err)
	}
	for _, line := range strings.Split(string(output), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == pgid && !strings.Contains(fields[1], "Z") {
			return true
		}
	}
	return false
}
