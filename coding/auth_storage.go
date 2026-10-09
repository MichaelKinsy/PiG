package coding

import (
	"encoding/json"

	"github.com/MichaelKinsy/PiG/ai"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// Ports packages/coding-agent/src/core/auth-storage.ts (readStoredCredential, exported from the package index, index.ts:26).

// ReadStoredCredential is a one-off synchronous read of providerID's credential from an auth.json file, without instantiating a store or resolving
// configured key values (auth-storage.ts:496). authPath defaults to the agent directory's auth.json. It returns nil, as upstream returns undefined, for
// a missing or malformed file or an absent entry. An entry that is not a credential object is also nil: upstream hands back whatever JSON the file
// holds there, which its Credential type does not admit.
func ReadStoredCredential(providerID string, authPath ...string) *ai.Credential {
	path := ""
	if len(authPath) > 0 {
		path = authPath[0]
	}
	entry := icodingagent.ReadStoredCredentialEntry(providerID, path)
	if entry == nil {
		return nil
	}
	var credential ai.Credential
	if json.Unmarshal(entry, &credential) != nil {
		return nil
	}
	return &credential
}
