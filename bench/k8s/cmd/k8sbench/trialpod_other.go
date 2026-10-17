//go:build !unix

package main

import (
	"os"
	"syscall"
)

// Trials run in Linux pods; elsewhere only the direct child is killed.
func groupAttr() *syscall.SysProcAttr { return nil }

func killGroup(p *os.Process) { _ = p.Kill() }
