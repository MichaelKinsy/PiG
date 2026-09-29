//go:build !linux && !windows

package nodespawn

import "runtime"

// kernelArch is runtime.GOARCH: only Linux applies the kernel's ELF rules.
func kernelArch() string { return runtime.GOARCH }

// kernelReadsLargeProgramHeaders is false: only Linux applies the kernel's ELF
// rules.
func kernelReadsLargeProgramHeaders() bool { return false }
