package coding

// Ports packages/coding-agent/src/core/client-identity.ts.

import "github.com/MichaelKinsy/PiG/ai"

// getClientIdentity is how PiG's requests identify it to providers: a detailed `User-Agent` with PiG's version, and attribution to PiG as a CLI agent.
func getClientIdentity() *ai.ClientIdentity {
	return &ai.ClientIdentity{
		UserAgent:   new(ai.UserAgentDetailed),
		Version:     new(Version),
		Attribution: new(ai.PiAttribution),
	}
}
