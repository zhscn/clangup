//go:build !unix

package cmk

import "os/exec"

func runCommandWithSignals(cmd *exec.Cmd) error { return cmd.Run() }

func commandSignalExitCode(error) (int, bool) { return 0, false }
