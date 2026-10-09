package mcp

import (
	"bytes"
	"encoding/json"
)

// Ports packages/mcp/src/protocol/types.ts.

// LatestProtocolVersion is the protocol version the client requests.
const LatestProtocolVersion = "2025-11-25"

// SupportedProtocolVersions are the versions the client accepts from a server.
// Servers that do not support the requested version answer with their own
// latest one, so older versions stay accepted for servers built on older SDKs.
var SupportedProtocolVersions = []SupportedProtocolVersion{LatestProtocolVersion, "2025-06-18", "2025-03-26", "2024-11-05"}

// SupportedProtocolVersion is one of SupportedProtocolVersions (types.ts:10).
type SupportedProtocolVersion string

// Implementation names a client or server implementation.
type Implementation struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Title   string `json:"title,omitempty"`
}

// Root is a filesystem root the client offers to servers.
type Root struct {
	URI  string `json:"uri"`
	Name string `json:"name,omitempty"`
}

// ListChangedCapability is a capability with an optional listChanged flag.
type ListChangedCapability struct {
	ListChanged *bool `json:"listChanged,omitempty"`
}

// ResourcesCapability is the server's resources capability.
type ResourcesCapability struct {
	Subscribe   *bool `json:"subscribe,omitempty"`
	ListChanged *bool `json:"listChanged,omitempty"`
}

// ClientCapabilities are the capabilities the client declares in initialize.
type ClientCapabilities struct {
	Experimental map[string]any         `json:"experimental,omitempty"`
	Roots        *ListChangedCapability `json:"roots,omitempty"`
	Sampling     map[string]any         `json:"sampling,omitempty"`
	Elicitation  map[string]any         `json:"elicitation,omitempty"`
}

// MarshalJSON emits a non-nil empty map as an empty object: `{sampling: {}}` declares the capability, and encoding/json's
// omitempty would drop it.
func (c ClientCapabilities) MarshalJSON() ([]byte, error) {
	wire := struct {
		Experimental any                    `json:"experimental,omitempty"`
		Roots        *ListChangedCapability `json:"roots,omitempty"`
		Sampling     any                    `json:"sampling,omitempty"`
		Elicitation  any                    `json:"elicitation,omitempty"`
	}{Roots: c.Roots}
	if c.Experimental != nil {
		wire.Experimental = c.Experimental
	}
	if c.Sampling != nil {
		wire.Sampling = c.Sampling
	}
	if c.Elicitation != nil {
		wire.Elicitation = c.Elicitation
	}
	return json.Marshal(wire)
}

// ServerCapabilities are the capabilities a server declares. A non-nil field
// means the server declared the capability, even as an empty object.
type ServerCapabilities struct {
	Experimental map[string]any         `json:"experimental,omitempty"`
	Logging      map[string]any         `json:"logging,omitempty"`
	Prompts      *ListChangedCapability `json:"prompts,omitempty"`
	Resources    *ResourcesCapability   `json:"resources,omitempty"`
	Tools        *ListChangedCapability `json:"tools,omitempty"`
	Completions  map[string]any         `json:"completions,omitempty"`
}

// InitializeParams are the params of the initialize request.
type InitializeParams struct {
	ProtocolVersion string             `json:"protocolVersion"`
	Capabilities    ClientCapabilities `json:"capabilities"`
	ClientInfo      Implementation     `json:"clientInfo"`
}

// InitializeResult is the result of the initialize request.
type InitializeResult struct {
	ProtocolVersion string             `json:"protocolVersion"`
	Capabilities    ServerCapabilities `json:"capabilities"`
	ServerInfo      Implementation     `json:"serverInfo"`
	Instructions    *string            `json:"instructions,omitempty"`
}

// ProgressNotification is the params of notifications/progress.
type ProgressNotification struct {
	ProgressToken JSONRPCID `json:"progressToken"`
	Progress      float64   `json:"progress"`
	Total         *float64  `json:"total,omitempty"`
	Message       string    `json:"message,omitempty"`
}

// CancelledNotification is the params of notifications/cancelled.
type CancelledNotification struct {
	RequestID JSONRPCID `json:"requestId"`
	Reason    string    `json:"reason,omitempty"`
}

// ToolAnnotations are a tool's hints. A hint that the server sent with a
// non-boolean value is left nil.
type ToolAnnotations struct {
	Title           string
	ReadOnlyHint    *bool
	DestructiveHint *bool
	IdempotentHint  *bool
	OpenWorldHint   *bool
}

// UnmarshalJSON reads the annotations and drops hints that are not booleans.
func (a *ToolAnnotations) UnmarshalJSON(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	*a = ToolAnnotations{}
	var title string
	if json.Unmarshal(fields["title"], &title) == nil {
		a.Title = title
	}
	hint := func(name string) *bool {
		var v bool
		switch string(bytes.TrimSpace(fields[name])) {
		case "true":
			v = true
		case "false":
		default:
			return nil
		}
		return &v
	}
	a.ReadOnlyHint, a.DestructiveHint = hint("readOnlyHint"), hint("destructiveHint")
	a.IdempotentHint, a.OpenWorldHint = hint("idempotentHint"), hint("openWorldHint")
	return nil
}

// MarshalJSON writes the annotations that are set.
func (a ToolAnnotations) MarshalJSON() ([]byte, error) {
	out := map[string]any{}
	if a.Title != "" {
		out["title"] = a.Title
	}
	for name, value := range map[string]*bool{
		"readOnlyHint": a.ReadOnlyHint, "destructiveHint": a.DestructiveHint,
		"idempotentHint": a.IdempotentHint, "openWorldHint": a.OpenWorldHint,
	} {
		if value != nil {
			out[name] = *value
		}
	}
	return json.Marshal(out)
}

// ToolExecution is a tool's execution metadata.
type ToolExecution struct {
	TaskSupport string `json:"taskSupport,omitempty"`
}

// Tool is a tool a server lists in tools/list. Fields the protocol does not
// define are not kept.
type Tool struct {
	Name  string `json:"name"`
	Title string `json:"title,omitempty"`
	// HasTitle is set when the server sent a non-null `title`, possibly empty. A title of a tool built in Go is present
	// when it is not empty.
	HasTitle     bool             `json:"-"`
	Description  string           `json:"description,omitempty"`
	InputSchema  json.RawMessage  `json:"inputSchema"`
	OutputSchema json.RawMessage  `json:"outputSchema,omitempty"`
	Annotations  *ToolAnnotations `json:"annotations,omitempty"`
	Execution    *ToolExecution   `json:"execution,omitempty"`
	Meta         json.RawMessage  `json:"_meta,omitempty"`
}

// UnmarshalJSON reads a tool and records whether the server sent a `title`.
func (t *Tool) UnmarshalJSON(data []byte) error {
	type plain Tool
	var wire struct {
		plain
		Title *string `json:"title"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	*t = Tool(wire.plain)
	if wire.Title != nil {
		t.Title, t.HasTitle = *wire.Title, true
	}
	return nil
}

// ListToolsResult is one page of tools/list.
type ListToolsResult struct {
	Tools      []Tool          `json:"tools"`
	NextCursor string          `json:"nextCursor,omitempty"`
	Meta       json.RawMessage `json:"_meta,omitempty"`
}

// Resource is a resource a server lists in resources/list.
type Resource struct {
	URI         string              `json:"uri"`
	Name        string              `json:"name"`
	Title       string              `json:"title,omitempty"`
	Description string              `json:"description,omitempty"`
	MimeType    string              `json:"mimeType,omitempty"`
	Size        *float64            `json:"size,omitempty"`
	Annotations *ContentAnnotations `json:"annotations,omitempty"`
	Meta        json.RawMessage     `json:"_meta,omitempty"`
	// Raw is the list entry as the server sent it, with `name` defaulted to the URI; empty for a Resource built in Go.
	Raw json.RawMessage `json:"-"`
}

// ResourceTemplate is a family of resources addressed by an RFC 6570 URI
// template, from resources/templates/list.
type ResourceTemplate struct {
	URITemplate string              `json:"uriTemplate"`
	Name        string              `json:"name"`
	Title       string              `json:"title,omitempty"`
	Description string              `json:"description,omitempty"`
	MimeType    string              `json:"mimeType,omitempty"`
	Annotations *ContentAnnotations `json:"annotations,omitempty"`
	Meta        json.RawMessage     `json:"_meta,omitempty"`
	// Raw is the list entry as the server sent it, with `name` defaulted to the URI template; empty for a
	// ResourceTemplate built in Go.
	Raw json.RawMessage `json:"-"`
}

// ListResourcesResult is one page of resources/list.
type ListResourcesResult struct {
	Resources  []Resource      `json:"resources"`
	NextCursor string          `json:"nextCursor,omitempty"`
	Meta       json.RawMessage `json:"_meta,omitempty"`
}

// ListResourceTemplatesResult is one page of resources/templates/list.
type ListResourceTemplatesResult struct {
	ResourceTemplates []ResourceTemplate `json:"resourceTemplates"`
	NextCursor        string             `json:"nextCursor,omitempty"`
	Meta              json.RawMessage    `json:"_meta,omitempty"`
}

// ReadResourceResult is the result of resources/read.
type ReadResourceResult struct {
	Contents []ResourceContents `json:"contents"`
	Meta     json.RawMessage    `json:"_meta,omitempty"`
}
