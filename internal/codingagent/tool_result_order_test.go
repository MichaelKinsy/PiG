package codingagent

import (
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/imageprocessing"
)

// Pi packages/coding-agent/src/utils/tool-result-images.ts:57-60 inserts each hint immediately after its image, never before it.
func TestNormalizedToolImageHintFollowsImage(t *testing.T) {
	result := imageprocessing.NormalizeToolResultImages(agent.AgentToolResult{
		Content: []ai.ToolResultMessageContent{ai.ImageContent{Data: "Qk06AAAAAAAAADYAAAAoAAAAAQAAAAEAAAABABgAAAAAAAQAAAAAAAAAAAAAAAAAAAAAAAAAAAD/AA==", MimeType: "image/bmp"}},
	}, false)
	blocks := ToolResultEventContent(result)
	var kinds []any
	for _, block := range blocks {
		kinds = append(kinds, block.(map[string]any)["type"])
	}
	if !reflect.DeepEqual(kinds, []any{"image", "text"}) {
		t.Fatalf("content kinds = %v, want [image text]", kinds)
	}
}
