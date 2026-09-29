package experimental

// Ports packages/coding-agent/src/experimental/server.ts (directory and identity selection).

import (
	"os"
	"path/filepath"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

const (
	EnvServerDir = "PIG_SERVER_DIR"
	EnvServerID  = "PIG_SERVER_ID"
)

// ResolveServerDirectory resolves an explicit path, the selected server-directory override, or the server directory beside the selected agent tree. Explicit empty paths resolve to the current directory.
func ResolveServerDirectory(directory *string) (string, error) {
	if directory != nil {
		return codingagent.ResolvePath(*directory, "")
	}
	name := EnvServerDir
	shared := codingagent.UsePiDirs()
	// pig divergence (D2): Pi directories and overrides require the process-level shared-directory opt-in.
	if shared {
		name = "PI_SERVER_DIR"
	}
	if configured, present := os.LookupEnv(name); present {
		return codingagent.ResolvePath(configured, "")
	}
	root := codingagent.ConfigRoot()
	if shared {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		root = filepath.Join(home, ".pi")
	}
	return codingagent.ResolvePath(filepath.Join(root, "server"), "")
}

func requestedServerId(serverID *string) *string {
	if serverID != nil {
		return serverID
	}
	name := EnvServerID
	// pig divergence (D2): server identity follows the selected configuration namespace, without falling back to the other product's override.
	if codingagent.UsePiDirs() {
		name = "PI_SERVER_ID"
	}
	if value, present := os.LookupEnv(name); present {
		return &value
	}
	return nil
}
