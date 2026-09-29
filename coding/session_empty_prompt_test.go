package coding

import (
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// Pi agent-session.ts:1721-1723,1882-1885,1899-1902 always starts prompt and queue content with a text block, even when its text is empty.
func TestBuildUserContentPreservesEmptyText(t *testing.T) {
	for _, images := range [][]ai.ImageContent{nil, {{Data: "aW1hZ2U=", MimeType: "image/png"}}} {
		want := ai.UserContentBlocks{ai.TextContent{Text: ""}}
		for _, image := range images {
			want = append(want, image)
		}
		if got := BuildUserContent("", images); !reflect.DeepEqual(got, want) {
			t.Fatalf("content=%#v want=%#v", got, want)
		}
	}
}
