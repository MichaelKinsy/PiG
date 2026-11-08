//go:build windows

package terminals

import "os/exec"

func detach(*exec.Cmd) {}
