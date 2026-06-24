//go:build !windows

package process

import "os/exec"

func configureHidden(_ *exec.Cmd) {}
