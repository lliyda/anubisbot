//go:build !windows

package panel

import "os/exec"

func hideConsole(cmd *exec.Cmd) {}
