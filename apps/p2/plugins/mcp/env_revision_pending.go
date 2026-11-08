package mcp

import (
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// connectionRevision is envs.ConnectionRevision (connection-revision.ts: the revision a connection to an environment was
// opened for: its own configuration's, and for a sandbox also its host's, so editing either starts a new connection).
// PENDING: fix/p2-envs (P2EnvsPorts) owns the real one; this stand-in hashes the same inputs and is replaced by the call
// when that branch merges.
func connectionRevision(envs *chordjson.Object, name string) string {
	value, _ := envs.Get(name)
	config, _ := value.(*chordjson.Object)
	revision := standInRevision(config)
	if config != nil && config.Value("type") != "ssh" {
		host, _ := config.Value("host").(string)
		other, _ := envs.Get(host)
		hostConfig, _ := other.(*chordjson.Object)
		revision += "+"
		if hostConfig != nil {
			revision += standInRevision(hostConfig)
		}
	}
	return revision
}

func standInRevision(config *chordjson.Object) string {
	encoded, err := chordjson.Marshal(config)
	if err != nil {
		panic(err)
	}
	return digest(string(encoded))
}
