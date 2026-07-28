// Package server_test exercises run.sh, the companion stack's launcher. It has
// no Go source of its own; the script is the thing under test.
package server_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// stack builds a sandbox for run.sh: stub go, bun and whisper binaries on PATH
// that record what ran, so the launcher's ordering and cleanup can be observed
// without a GPU, a model or a compiler.
type stack struct {
	t       *testing.T
	dir     string
	markers string
}

func newStack(t *testing.T) *stack {
	t.Helper()
	dir := t.TempDir()
	s := &stack{t: t, dir: dir, markers: filepath.Join(dir, "markers")}
	if err := os.MkdirAll(s.markers, 0o755); err != nil {
		t.Fatal(err)
	}
	// bun serves two roles: reading the soul's voice_ref, then running the
	// orchestrator. The orchestrator stub blocks so the script stays up.
	s.stub("bun", `
if [ "$1" = "-e" ]; then echo "/voices/test.wav"; exit 0; fi
touch "$MARKERS/bun-run"
sleep 30
`)
	s.stub("go", `touch "$MARKERS/go-build"`)
	s.stub("whisper-server", `
touch "$MARKERS/whisper-started"
trap 'touch "$MARKERS/whisper-stopped"; exit 0' TERM
sleep 30 &
wait
`)
	return s
}

func (s *stack) stub(name, body string) {
	s.t.Helper()
	path := filepath.Join(s.dir, name)
	script := "#!/usr/bin/env bash\nMARKERS=" + s.markers + "\n" + body
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		s.t.Fatal(err)
	}
}

func (s *stack) ran(marker string) bool {
	_, err := os.Stat(filepath.Join(s.markers, marker))
	return err == nil
}

func (s *stack) command() *exec.Cmd {
	cmd := exec.Command("bash", "run.sh")
	// Run in its own process group so the test can interrupt the whole stack
	// the way Ctrl-C does; bash defers trap handling until the foreground
	// command returns, so signalling bash alone would wait for it.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Env = append(os.Environ(),
		"PATH="+s.dir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"WHISPER_BIN="+filepath.Join(s.dir, "whisper-server"),
		"WHISPER_MODEL="+filepath.Join(s.dir, "model.bin"),
	)
	return cmd
}

// waitFor polls for a marker; the stubs are separate processes, so their side
// effects are not immediate.
func (s *stack) waitFor(marker string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if s.ran(marker) {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

// TestBuildFailureStartsNothing is the regression guard for a launcher that
// started Whisper first and only installed its cleanup trap afterwards: a
// compile error then exited under `set -e` and orphaned the Whisper process.
func TestBuildFailureStartsNothing(t *testing.T) {
	s := newStack(t)
	s.stub("go", `echo "cmd/tts-server/main.go:1:1: syntax error" >&2; exit 1`)

	output, err := s.command().CombinedOutput()
	if err == nil {
		t.Fatalf("run.sh should fail when the build fails; output: %s", output)
	}
	if !strings.Contains(string(output), "syntax error") {
		t.Errorf("the compiler's message should reach the user, got: %s", output)
	}
	if s.ran("whisper-started") {
		t.Error("Whisper must not be launched before the build succeeds")
	}
	if s.ran("bun-run") {
		t.Error("the orchestrator must not start when the build fails")
	}
}

// TestCleanupStopsChildren checks the other half: once children are running,
// leaving the script for any reason takes them down.
func TestCleanupStopsChildren(t *testing.T) {
	s := newStack(t)
	cmd := s.command()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		_, _ = cmd.Process.Wait()
	}()

	if !s.waitFor("go-build", 10*time.Second) {
		t.Fatal("the build never ran")
	}
	if !s.waitFor("whisper-started", 10*time.Second) {
		t.Fatal("Whisper never started")
	}
	if !s.waitFor("bun-run", 10*time.Second) {
		t.Fatal("the orchestrator never started")
	}

	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	_, _ = cmd.Process.Wait()
	if !s.waitFor("whisper-stopped", 10*time.Second) {
		t.Error("Whisper was left running after run.sh exited")
	}
}
