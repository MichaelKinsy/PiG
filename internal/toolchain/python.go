package toolchain

import "strings"

// pig additive (D19): PiG runs Python extensions, which Pi does not, and every launch and packed-cell key resolves the interpreter here.
//
// PythonExecutable returns the Python interpreter for goos: the first of python and python3 on Windows, or python3
// elsewhere, that lookPath finds. On Windows a clean profile resolves python.exe to the Microsoft Store app-execution
// alias, a shim that opens the Store instead of running Python, so a real interpreter later in the candidate list wins.
// The alias is the last resort because, once the Store package is installed, the same path is a working Python. When
// no candidate is found it returns the bare first candidate name, so the launch reports the missing interpreter.
func PythonExecutable(goos string, lookPath func(string) (string, error)) string {
	candidates := pythonExecutableCandidates(goos)
	var alias string
	for _, candidate := range candidates {
		path, err := lookPath(candidate)
		if err != nil {
			continue
		}
		if goos == "windows" && isWindowsStoreAlias(path) {
			if alias == "" {
				alias = path
			}
			continue
		}
		return path
	}
	if alias != "" {
		return alias
	}
	return candidates[0]
}

func pythonExecutableCandidates(goos string) []string {
	if goos == "windows" {
		return []string{"python", "python3"}
	}
	return []string{"python3"}
}

// isWindowsStoreAlias reports whether path lies in a Microsoft\WindowsApps directory, where Windows places the
// per-user app-execution aliases. Windows paths compare without regard to case.
func isWindowsStoreAlias(path string) bool {
	normalized := strings.ToLower(strings.ReplaceAll(path, `\`, "/"))
	return strings.Contains(normalized, "/microsoft/windowsapps/")
}
