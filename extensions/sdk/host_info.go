package sdk

import (
	"encoding/json"
	"errors"
	"os"
)

// hostInfoEnv is the environment variable the host starts every extension process with: Pi's getAgentDir(), CONFIG_DIR_NAME and VERSION (config.ts) as the
// host resolves them. They are callable while an extension's factory runs, before the ready message, so they travel in the environment.
const hostInfoEnv = "PIG_EXT_HOST_INFO"

// ErrHostInfoUnavailable reports that this process was not started by a PiG host that provides its configuration facts.
var ErrHostInfoUnavailable = errors.New("sdk: the PiG host did not provide its configuration facts (PIG_EXT_HOST_INFO)")

type hostInfo struct {
	AgentDir      string `json:"agentDir"`
	ConfigDirName string `json:"configDirName"`
	Version       string `json:"version"`
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
