package sdk

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// ReadStoredCredential is Pi's readStoredCredential (core/auth-storage.ts, exported from index.ts): a one-off synchronous read of the credential stored for providerID in an
// auth.json file, without a store and without resolving configured key values. authPath defaults to auth.json in the agent directory ([AgentDir]); a leading ~ in it is the home
// directory and a file:// URL is its path, as Pi's normalizePath reads them. A file that cannot be read or is not a JSON object, and a provider it has no credential for, give
// nil, and a stored value that is not an object is an error (Pi hands back whatever the file holds). The credential keeps the key order of the file. The error is [ErrHostInfoUnavailable] when there is no authPath and the host gave no agent directory, which Pi's default
// argument throws.
func ReadStoredCredential(providerID string, authPath ...string) (*Credential, error) {
	path := ""
	if len(authPath) > 0 {
		path = authPath[0]
	} else {
		dir, err := AgentDir()
		if err != nil {
			return nil, err
		}
		path = filepath.Join(dir, "auth.json")
	}
	content, err := os.ReadFile(normalizeStoredPath(path))
	if err != nil {
		return nil, nil
	}
	content = bytes.TrimPrefix(content, []byte("\xef\xbb\xbf"))
	var data map[string]json.RawMessage
	if json.Unmarshal(content, &data) != nil {
		return nil, nil
	}
	raw, ok := data[providerID]
	if !ok || string(raw) == "null" {
		return nil, nil
	}
	credential := &Credential{}
	if err := json.Unmarshal(raw, credential); err != nil {
		return nil, errors.New("the stored credential for " + providerID + " is not a JSON object")
	}
	return credential, nil
}

// normalizeStoredPath is the part of Pi's normalizePath (utils/paths.ts) that readStoredCredential's path can use: a leading ~ is the home directory and a file:// URL is its path.
func normalizeStoredPath(path string) string {
	if path == "~" || strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(path, "~"))
		}
	}
	if rest, ok := strings.CutPrefix(path, "file://"); ok {
		return rest
	}
	return path
}
