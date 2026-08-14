//go:build unix

package cmk

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
)

// commandSignalError records the signal that stopped a command tree. Run uses
// it to preserve the conventional 128+signal exit status without signalling
// itself before the child has been reaped.
type commandSignalError struct {
	signal syscall.Signal
}

func (e *commandSignalError) Error() string { return fmt.Sprintf("signal: %s", e.signal) }

func runCommandWithSignals(cmd *exec.Cmd) error {
	// Keep the tool and its descendants out of cmk's process group. This lets
	// cmk forward a targeted signal without also signalling its parent shell.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	signals := make(chan os.Signal, 4)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(signals)
	if err := cmd.Start(); err != nil {
		return err
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	var received syscall.Signal
	for {
		select {
		case sig := <-signals:
			s, ok := sig.(syscall.Signal)
			if !ok {
				continue
			}
			if received == 0 {
				received = s
			}
			// The child is the process-group leader. Build tools such as Ninja
			// receive the signal here and clean up any compiler groups they own.
			if err := syscall.Kill(-cmd.Process.Pid, s); err != nil && err != syscall.ESRCH {
				// A direct signal still lets a cooperating build tool clean up its
				// descendants if process-group signalling is unavailable.
				_ = cmd.Process.Signal(s)
			}
		case err := <-done:
			// Stop delivery before draining so a signal that raced with Wait is
			// either already queued or handled by the process's default action.
			signal.Stop(signals)
			select {
			case sig := <-signals:
				if s, ok := sig.(syscall.Signal); ok && received == 0 {
					received = s
				}
			default:
			}
			if received != 0 {
				return &commandSignalError{signal: received}
			}
			return err
		}
	}
}

func commandSignalExitCode(err error) (int, bool) {
	var signalErr *commandSignalError
	if !errors.As(err, &signalErr) {
		return 0, false
	}
	return 128 + int(signalErr.signal), true
}
