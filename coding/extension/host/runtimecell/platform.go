package runtimecell

import "strings"

func packedRunnerName(goos, language string) string {
	switch strings.ToLower(language) {
	case "python":
		return "runner.py"
	case "go", "rust", "lisp":
		if goos == "windows" {
			return "runner.exe"
		}
		return "runner"
	default:
		return "runner"
	}
}
