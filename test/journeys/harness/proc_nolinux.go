//go:build !linux

package harness

// ProcTreeRSS is only measured on Linux.
func ProcTreeRSS(pid int) (total, mainHWM int64) { return 0, 0 }

func findMarked(marker string) []int { return nil }

func procCmdline(pid int) string { return "" }
