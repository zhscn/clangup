//go:build unix

package cmk

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestRunStreamingSignalHelper(t *testing.T) {
	if os.Getenv("CMK_SIGNAL_HELPER") != "1" {
		return
	}
	err := runStreaming(os.Environ(), "ninja", "-C", os.Getenv("CMK_SIGNAL_BUILD_DIR"))
	if code, ok := commandSignalExitCode(err); ok {
		os.Exit(code)
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Fatal("ninja exited without a signal")
}

func TestRunStreamingTerminatesNinjaDescendants(t *testing.T) {
	if _, err := exec.LookPath("ninja"); err != nil {
		t.Skip("ninja not found")
	}

	dir := t.TempDir()
	pidsFile := filepath.Join(dir, "pids")
	script := filepath.Join(dir, "slow.sh")
	if err := os.WriteFile(script, []byte(`#!/bin/sh
printf '%s %s\n' "$PPID" "$$" > "$1"
trap 'exit 0' HUP INT TERM
while :; do sleep 1; done
`), 0o755); err != nil {
		t.Fatal(err)
	}
	build := fmt.Sprintf("rule slow\n  command = %s %s\nbuild all: slow\ndefault all\n", script, pidsFile)
	if err := os.WriteFile(filepath.Join(dir, "build.ninja"), []byte(build), 0o644); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	cmd := exec.Command(os.Args[0], "-test.run=^TestRunStreamingSignalHelper$")
	cmd.Env = append(os.Environ(), "CMK_SIGNAL_HELPER=1", "CMK_SIGNAL_BUILD_DIR="+dir)
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}

	var ninjaPID, childPID int
	t.Cleanup(func() {
		if ninjaPID != 0 {
			_ = syscall.Kill(-ninjaPID, syscall.SIGKILL)
		}
		if childPID != 0 {
			_ = syscall.Kill(-childPID, syscall.SIGKILL)
		}
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	})

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(pidsFile)
		if err == nil {
			fields := strings.Fields(string(data))
			if len(fields) == 2 {
				ninjaPID, _ = strconv.Atoi(fields[0])
				childPID, _ = strconv.Atoi(fields[1])
				if ninjaPID > 0 && childPID > 0 {
					break
				}
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if ninjaPID == 0 || childPID == 0 {
		t.Fatalf("Ninja descendant did not start: %s", output.String())
	}

	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	err := cmd.Wait()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 128+int(syscall.SIGTERM) {
		t.Fatalf("helper exit = %v, want status %d; output: %s", err, 128+int(syscall.SIGTERM), output.String())
	}

	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && processExists(childPID) {
		time.Sleep(20 * time.Millisecond)
	}
	if processExists(childPID) {
		t.Fatalf("Ninja descendant %d survived SIGTERM; output: %s", childPID, output.String())
	}
}

func processExists(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}
