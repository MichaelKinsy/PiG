// SPDX-License-Identifier: MIT

//go:build !linux

package main

import "errors"

// run measures through /proc and a Linux pseudo-terminal.
func run([]string) error { return errors.New("run: measuring needs Linux (/proc and /dev/ptmx)") }
