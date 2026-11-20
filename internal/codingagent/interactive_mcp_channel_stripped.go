//go:build pig_strip_mcp

package codingagent

import "github.com/MichaelKinsy/PiG/tui"

func mcpChannelRenderer(CustomMessageEntry) tui.MessageRenderer { return nil }
