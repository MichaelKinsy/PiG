package sdk

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"unicode/utf8"
)

// hostInfoEnv is the environment variable the host starts every extension process with: Pi's getAgentDir(), CONFIG_DIR_NAME, VERSION and getPackageDir() (config.ts) and
// the default getShellConfig() (utils/shell.ts) as the host resolves them. They are callable while an extension's factory runs, before the ready message, so they travel in the environment.
const hostInfoEnv = "PIG_EXT_HOST_INFO"

// ErrHostInfoUnavailable reports that this process was not started by a PiG host that provides its configuration facts.
var ErrHostInfoUnavailable = errors.New("sdk: the PiG host did not provide its configuration facts (PIG_EXT_HOST_INFO)")

type hostInfo struct {
	AgentDir      string `json:"agentDir"`
	ConfigDirName string `json:"configDirName"`
	Version       string `json:"version"`
	PackageDir    string `json:"packageDir"`
	Shell         *struct {
		Shell            string   `json:"shell"`
		Args             []string `json:"args"`
		CommandTransport string   `json:"commandTransport"`
		Error            string   `json:"error"`
	} `json:"shell"`
}

func readHostInfo() (hostInfo, error) {
	raw := os.Getenv(hostInfoEnv)
	if raw == "" {
		return hostInfo{}, ErrHostInfoUnavailable
	}
	var info hostInfo
	if err := json.Unmarshal([]byte(raw), &info); err != nil {
		return hostInfo{}, errors.Join(ErrHostInfoUnavailable, err)
	}
	return info, nil
}

// AgentDir is Pi's getAgentDir(): the agent configuration directory the host runs with (for PiG its own tree, not Pi's). It returns
// [ErrHostInfoUnavailable] when the process was not started by a host that provides it, or when the host could not resolve the directory.
func AgentDir() (string, error) {
	info, err := readHostInfo()
	if err != nil {
		return "", err
	}
	if info.AgentDir == "" {
		return "", ErrHostInfoUnavailable
	}
	return info.AgentDir, nil
}

// ConfigDirName is Pi's CONFIG_DIR_NAME: the per-project configuration directory name the host uses (".pig"; ".pi" when it shares Pi's tree).
func ConfigDirName() (string, error) {
	info, err := readHostInfo()
	if err != nil {
		return "", err
	}
	return info.ConfigDirName, nil
}

// Version is Pi's VERSION: the Pi version the host implements, which extensions compare against to gate features.
func Version() (string, error) {
	info, err := readHostInfo()
	if err != nil {
		return "", err
	}
	return info.Version, nil
}

// PackageDir is Pi's getPackageDir(): the install directory the host runs from. For PiG it is PI_PACKAGE_DIR when that is set, else the directory of the host
// executable, which is Pi's own answer for a compiled binary. It returns [ErrHostInfoUnavailable] when the process was not started by a host that provides it, or the
// host could not resolve it.
func PackageDir() (string, error) {
	info, err := readHostInfo()
	if err != nil {
		return "", err
	}
	if info.PackageDir == "" {
		return "", ErrHostInfoUnavailable
	}
	return info.PackageDir, nil
}

// ShellConfig is Pi's ShellConfig (utils/shell.ts): the shell binary a command runs in and its leading arguments. The command is appended as the last argument unless
// CommandTransport is "stdin", in which case it is written to the shell's standard input (legacy WSL bash.exe).
type ShellConfig struct {
	Shell            string
	Args             []string
	CommandTransport string
}

// GetShellConfig is Pi's getShellConfig(customShellPath): the shell the bash tool runs commands in. A non-empty customShellPath names the shell and must exist ("Custom
// shell path not found" otherwise). With none, it is the host's own resolution: /bin/bash, then bash on PATH, then sh on Unix; Git Bash, then bash.exe on PATH on Windows,
// which is an error when none is found. It returns [ErrHostInfoUnavailable] when the host provides no default.
func GetShellConfig(customShellPath ...string) (ShellConfig, error) {
	if len(customShellPath) > 0 && customShellPath[0] != "" {
		if _, err := os.Stat(customShellPath[0]); err != nil {
			return ShellConfig{}, errors.New("Custom shell path not found: " + customShellPath[0])
		}
		return bashShellConfig(customShellPath[0]), nil
	}
	info, err := readHostInfo()
	if err != nil {
		return ShellConfig{}, err
	}
	if info.Shell == nil {
		return ShellConfig{}, ErrHostInfoUnavailable
	}
	if info.Shell.Error != "" {
		return ShellConfig{}, errors.New(info.Shell.Error)
	}
	return ShellConfig{Shell: info.Shell.Shell, Args: info.Shell.Args, CommandTransport: info.Shell.CommandTransport}, nil
}

// bashShellConfig is Pi's getBashShellConfig: the legacy WSL bash.exe under Windows\System32 reads its command from standard input. isLegacyWslBashPath lowercases
// the path with / as \ and tests /^[a-z]:\\windows\\(?:system32|sysnative)\\bash\.exe$/; only an ASCII letter, or the Kelvin sign that lowercases to k, can be the drive.
func bashShellConfig(shell string) ShellConfig {
	drive, size := utf8.DecodeRuneInString(shell)
	legacy := drive >= 'a' && drive <= 'z' || drive >= 'A' && drive <= 'Z' || drive == 0x212A
	if legacy {
		rest := strings.Map(func(r rune) rune {
			if r == '/' {
				return '\\'
			}
			return r
		}, shell[size:])
		legacy = asciiFold(rest, `:\windows\system32\bash.exe`) || asciiFold(rest, `:\windows\sysnative\bash.exe`)
	}
	if legacy {
		return ShellConfig{Shell: shell, Args: []string{"-s"}, CommandTransport: "stdin"}
	}
	return ShellConfig{Shell: shell, Args: []string{"-c"}}
}

// asciiFold reports whether text is want with its ASCII letters in any case.
func asciiFold(text, want string) bool {
	if len(text) != len(want) {
		return false
	}
	for i := range len(want) {
		a, b := text[i], want[i]
		if a >= 'A' && a <= 'Z' {
			a += 'a' - 'A'
		}
		if a != b {
			return false
		}
	}
	return true
}
