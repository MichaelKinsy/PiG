package codingagent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"

	"github.com/MichaelKinsy/PiG/internal/resolvepath"
	"github.com/MichaelKinsy/PiG/internal/text"
)

// Ports packages/coding-agent/src/core/auth-storage.ts (readStoredCredential).

// ReadStoredCredentialEntry is the one-off read of readStoredCredential (auth-storage.ts:496-506): the entry of providerID in the auth.json at authPath
// (the agent directory's auth.json when authPath is empty), as written, without instantiating a store or resolving configured key values. A missing or
// malformed file, an absent entry and a JavaScript-falsy entry (null, false, 0, "") yield nil: a caller that tests `if (credential)` treats them alike.
func ReadStoredCredentialEntry(providerID, authPath string) json.RawMessage {
	if authPath == "" {
		authPath = filepath.Join(AgentDir(), "auth.json")
	}
	return ReadStoredCredentialEntryAt(providerID, authPath)
}

// ReadStoredCredentialEntryAt is [ReadStoredCredentialEntry] for a path the caller passed explicitly: an empty path is not the default, because the default parameter applies only to an omitted argument, so reading "" fails and yields nil.
func ReadStoredCredentialEntryAt(providerID, authPath string) json.RawMessage {
	// normalizePath: a leading ~ expands and a file:// URL becomes its path; an invalid URL throws inside upstream's try and reads as absent.
	path, err := resolvepath.Normalize(authPath)
	if err != nil {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var entries map[string]json.RawMessage
	if json.Unmarshal(text.StripBomBytes(data), &entries) != nil {
		return nil
	}
	entry := entries[providerID]
	switch string(entry) {
	case "null", "false", `""`:
		return nil
	}
	//portlint:allow numbers entry is a JSON value from Unmarshal and every JSON number is in ParseFloat's grammar, so this is JS truthiness of 0, -0 and underflow to 0 (auth-storage.ts:496-506)
	if number, err := strconv.ParseFloat(string(entry), 64); err == nil && number == 0 {
		return nil
	}
	return entry
}
