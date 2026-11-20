// Package mcpchannel carries host-only attribution for validated MCP pushes.
package mcpchannel

const (
	CustomType = "mcp-channel"
	SourceKey  = "mcpChannelSource"
)

// Source is set by the built-in adapter after opt-in, capability and payload
// validation. The zero value denotes an ordinary extension message.
type Source struct{ name string }

func ValidatedSource(name string) Source { return Source{name: name} }
func (s Source) Name() string            { return s.name }

// MessageSource reads the host-populated field, never an extension's Details.
func MessageSource(custom map[string]any) string {
	source, _ := custom[SourceKey].(string)
	return source
}

// Attribute marks a host-admitted push. The name alone has no effect.
func (s Source) Attribute(custom map[string]any) bool {
	if s.name == "" || custom["customType"] != CustomType {
		return false
	}
	custom[SourceKey] = s.name
	return true
}
