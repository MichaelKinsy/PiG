package runtime

import "github.com/MichaelKinsy/PiG/schema"

// Ports apps/p2/src/runtime/session-lock.ts: a session's lock. A locked session (one listening to a Discord channel,
// say) uses only the resources its owner picked, and everyone who can send it input may use all of them. Kept by the
// sessions plugin on the server (`SessionLocks`), mirrored into the session's Durable state by its worker, where the
// tool guard, the prompt and MCP placement read it.

// LockCapability is a capability a locked session may get besides MCP servers and instructions.
type LockCapability string

// The lock capabilities (`LOCK_CAPABILITIES`).
const (
	CapabilityPorts         LockCapability = "ports"
	CapabilityConversations LockCapability = "conversations"
)

// LockCapabilities lists every capability.
var LockCapabilities = []LockCapability{CapabilityPorts, CapabilityConversations}

// SessionLockSchema is a session's lock: the picked MCP servers of the owner's mcp.json by name, whether the servers of
// `.pi/mcp.json` at the session's locations are allowed, whether the global AGENTS.md, skills and prompt templates
// are, and the capabilities.
var SessionLockSchema = schema.Obj(schema.Properties{
	schema.Prop("mcp", schema.Arr(schema.Str(schema.MaxLength(200)), schema.MaxItems(200))),
	schema.Prop("projectMcp", schema.Bool()),
	schema.Prop("instructions", schema.Bool()),
	schema.Prop("capabilities", schema.Arr(schema.Union([]schema.Schema{Literal("ports"), Literal("conversations")}), schema.MaxItems(8))),
})

// SessionLock is a session's lock (`SessionLockSchema`).
type SessionLock struct {
	MCP          []string         `json:"mcp"`
	ProjectMCP   bool             `json:"projectMcp"`
	Instructions bool             `json:"instructions"`
	Capabilities []LockCapability `json:"capabilities"`
}
