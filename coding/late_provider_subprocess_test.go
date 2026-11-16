package coding

import (
	"github.com/MichaelKinsy/PiG/ai"
)

func lateProviderText(message *ai.AssistantMessage) string {
	var text string
	for _, block := range message.Content {
		if part, ok := block.(ai.TextContent); ok {
			text += part.Text
		}
	}
	return text
}
