//go:build !unix

package main

import "syscall"

func procGroup() *syscall.SysProcAttr { return nil }
