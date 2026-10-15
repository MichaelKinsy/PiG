//go:build unix

package main

import "syscall"

func procGroup() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setpgid: true} }
