package mcpext

import (
	"encoding/json"
	"html"
	"sort"
	"strings"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/mcpchannel"
)

// ChannelMaxBytes bounds the complete notification params, before decoding.
// pig additive (D657): explicit opt-in pushes have a documented one-MiB budget.
const ChannelMaxBytes = 1 << 20

// channelContentEscaper leaves quotes verbatim in text while preventing content from closing the channel wrapper.
var channelContentEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")

// channelMessage preserves provenance independently of the visible envelope. A
// literal user-typed envelope remains a user entry, never an mcp-channel entry.
func channelMessage(source string, params json.RawMessage) (extension.SendMessagePayload, bool) {
	if len(params) > ChannelMaxBytes {
		return extension.SendMessagePayload{}, false
	}
	var payload struct {
		Content *string                    `json:"content"`
		Meta    map[string]json.RawMessage `json:"meta"`
	}
	if json.Unmarshal(params, &payload) != nil || payload.Content == nil {
		return extension.SendMessagePayload{}, false
	}
	meta := map[string]string{}
	keys := []string{}
	for key, raw := range payload.Meta {
		if key == "" || key == "source" || strings.IndexFunc(key, func(r rune) bool {
			return !(r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_')
		}) >= 0 {
			continue
		}
		var value *string
		if json.Unmarshal(raw, &value) == nil && value != nil {
			meta[key] = *value
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	var text strings.Builder
	text.WriteString(`<channel source="` + html.EscapeString(source) + `"`)
	for _, key := range keys {
		text.WriteString(` ` + key + `="` + html.EscapeString(meta[key]) + `"`)
	}
	text.WriteString(">" + channelContentEscaper.Replace(*payload.Content) + "</channel>")
	return extension.SendMessagePayload{
		CustomType: mcpchannel.CustomType, Content: text.String(), Display: true,
		Details: map[string]any{"source": source, "meta": meta},
	}, true
}

// channelSender is optional so embedded/headless hosts that cannot start turns
// continue to work. Delivery is synchronous admission only, not a model call.
type channelSender interface {
	SendChannel(extension.SendMessagePayload, *extension.SendMessageOptions)
}

func (h apiHost) SendChannel(message extension.SendMessagePayload, options *extension.SendMessageOptions) {
	if sender, ok := h.api.(channelSender); ok {
		sender.SendChannel(message, options)
	}
}

func (a *piAdapter) SendChannel(message extension.SendMessagePayload, options *extension.SendMessageOptions) {
	a.pi.SendMessage(message, options)
}

func channelOptions(source string) *extension.SendMessageOptions {
	return &extension.SendMessageOptions{
		TriggerTurn: new(true), DeliverAs: extension.DeliverAsSteer, MCPChannel: mcpchannel.ValidatedSource(source),
	}
}
