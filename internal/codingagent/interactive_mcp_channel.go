//go:build !pig_strip_mcp

package codingagent

import (
	"github.com/MichaelKinsy/PiG/coding/mcpext/mcptui"
	"github.com/MichaelKinsy/PiG/internal/mcpchannel"
	"github.com/MichaelKinsy/PiG/tui"
)

func mcpChannelRenderer(entry CustomMessageEntry) tui.MessageRenderer {
	// pig additive (D657): only host attribution selects the channel style.
	// Keep the ordinary extension renderer namespace independent of this path.
	if entry.MCPChannelSource == "" || entry.CustomType != mcpchannel.CustomType {
		return nil
	}
	return func(_ *tui.CustomMessage, options tui.MessageRenderOptions) tui.Component {
		return mcptui.RenderChannel(entry.MCPChannelSource, entry.Content, options.OutputPad)
	}
}
