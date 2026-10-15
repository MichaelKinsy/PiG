package turn

import (
	"testing"

	"github.com/MichaelKinsy/PiG/durable/core/jv"
)

func BenchmarkClassifyFailedMessage(b *testing.B) {
	message := jv.NewObject()
	message.Set("stopReason", "error")
	message.Set("errorMessage", "503 service unavailable: upstream connect error")
	message.Set("provider", "openai")
	for b.Loop() {
		IsContextOverflow(message)
		IsRetryableAssistantError(message)
	}
}
