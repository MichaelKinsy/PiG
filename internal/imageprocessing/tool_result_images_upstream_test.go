package imageprocessing

import (
	"bytes"
	"compress/zlib"
	"encoding/base64"
	"encoding/binary"
	"hash/crc32"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

const toolResultTinyPNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8DwHwAFBQIAX8jx0gAAAABJRU5ErkJggg=="

// Same 8-bit grayscale rows, filter bytes, dimensions and PNG chunks as packages/coding-agent/test/tool-result-images.test.ts:10-35.
func toolResultPNG(t testing.TB, width, height int) ai.ImageContent {
	t.Helper()
	var out bytes.Buffer
	out.Write([]byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a})
	chunk := func(kind string, body []byte) {
		var size [4]byte
		binary.BigEndian.PutUint32(size[:], uint32(len(body)))
		out.Write(size[:])
		out.WriteString(kind)
		out.Write(body)
		checksum := crc32.NewIEEE()
		_, _ = checksum.Write([]byte(kind))
		_, _ = checksum.Write(body)
		binary.BigEndian.PutUint32(size[:], checksum.Sum32())
		out.Write(size[:])
	}
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr, uint32(width))
	binary.BigEndian.PutUint32(ihdr[4:], uint32(height))
	ihdr[8] = 8
	raw := make([]byte, (width+1)*height)
	for row := range height {
		for col := 1; col <= width; col++ {
			raw[row*(width+1)+col] = byte(row % 256)
		}
	}
	var compressed bytes.Buffer
	writer := zlib.NewWriter(&compressed)
	if _, err := writer.Write(raw); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	chunk("IHDR", ihdr)
	chunk("IDAT", compressed.Bytes())
	chunk("IEND", nil)
	return ai.ImageContent{Data: base64.StdEncoding.EncodeToString(out.Bytes()), MimeType: "image/png"}
}

// Exact 58-byte, 1x1 red 24bpp fixture from tool-result-images.test.ts:42-58.
func toolResultBMP() ai.ImageContent {
	data := make([]byte, 58)
	copy(data, "BM")
	binary.LittleEndian.PutUint32(data[2:], 58)
	binary.LittleEndian.PutUint32(data[10:], 54)
	binary.LittleEndian.PutUint32(data[14:], 40)
	binary.LittleEndian.PutUint32(data[18:], 1)
	binary.LittleEndian.PutUint32(data[22:], 1)
	binary.LittleEndian.PutUint16(data[26:], 1)
	binary.LittleEndian.PutUint16(data[28:], 24)
	binary.LittleEndian.PutUint32(data[34:], 4)
	data[56] = 0xff
	return ai.ImageContent{Data: base64.StdEncoding.EncodeToString(data), MimeType: "image/bmp"}
}

// Ports all seven case sites in packages/coding-agent/test/tool-result-images.test.ts:65-145. The production Session normalizer owns this same result.Content slice.
func TestUpstreamNormalizeToolResultImages(t *testing.T) {
	normalize := func(content []ai.ToolResultMessageContent, resize bool) []ai.ToolResultMessageContent {
		return NormalizeToolResultImages(agent.AgentToolResult{Content: content}, resize).Content
	}
	same := func(t *testing.T, got, want []ai.ToolResultMessageContent) {
		t.Helper()
		if len(got) != len(want) || &got[0] != &want[0] {
			t.Fatal("unchanged content did not retain its original slice")
		}
	}
	t.Run("returns the original array when there are no image blocks", func(t *testing.T) {
		content := []ai.ToolResultMessageContent{ai.TextContent{Text: "no images here"}}
		same(t, normalize(content, true), content)
	})
	t.Run("returns the original array when images are already within limits", func(t *testing.T) {
		content := []ai.ToolResultMessageContent{ai.TextContent{Text: "screenshot"}, ai.ImageContent{Data: toolResultTinyPNG, MimeType: "image/png"}}
		same(t, normalize(content, true), content)
	})
	t.Run("resizes oversized images and reports the original dimensions", func(t *testing.T) {
		content := []ai.ToolResultMessageContent{toolResultPNG(t, 2400, 4800)}
		normalized := normalize(content, true)
		if len(normalized) != 2 || &normalized[0] == &content[0] {
			t.Fatalf("normalized length=%d or reused input", len(normalized))
		}
		image, ok := normalized[0].(ai.ImageContent)
		if !ok {
			t.Fatalf("first block=%T", normalized[0])
		}
		data, err := base64.StdEncoding.DecodeString(image.Data)
		if err != nil {
			t.Fatal(err)
		}
		if len(data) < 24 || binary.BigEndian.Uint32(data[16:]) > 2000 || binary.BigEndian.Uint32(data[20:]) > 2000 {
			t.Fatal("image dimensions exceed limits")
		}
		note, ok := normalized[1].(ai.TextContent)
		if !ok || !strings.Contains(note.Text, "original 2400x4800") {
			t.Fatalf("note=%#v", normalized[1])
		}
	})
	t.Run("leaves oversized images alone when auto-resize is disabled", func(t *testing.T) {
		content := []ai.ToolResultMessageContent{toolResultPNG(t, 2400, 4800)}
		same(t, normalize(content, false), content)
	})
	t.Run("converts unsupported image formats even when auto-resize is disabled", func(t *testing.T) {
		content := []ai.ToolResultMessageContent{toolResultBMP()}
		normalized := normalize(content, false)
		if len(normalized) != 2 || &normalized[0] == &content[0] {
			t.Fatalf("normalized length=%d or reused input", len(normalized))
		}
		image, ok := normalized[0].(ai.ImageContent)
		if !ok || image.MimeType != "image/png" {
			t.Fatalf("image=%#v", normalized[0])
		}
		if normalized[1] != (ai.TextContent{Text: "[Image converted from image/bmp to image/png.]"}) {
			t.Fatalf("note=%#v", normalized[1])
		}
	})
	t.Run("keeps undecodable images instead of dropping tool output", func(t *testing.T) {
		content := []ai.ToolResultMessageContent{ai.ImageContent{Data: "bm90LWFuLWltYWdl", MimeType: "image/png"}}
		same(t, normalize(content, true), content)
	})
	t.Run("preserves surrounding text blocks and their order", func(t *testing.T) {
		content := []ai.ToolResultMessageContent{ai.TextContent{Text: "before"}, toolResultPNG(t, 2400, 100), ai.TextContent{Text: "after"}}
		normalized := normalize(content, true)
		var kinds []string
		for _, block := range normalized {
			switch block.(type) {
			case ai.TextContent:
				kinds = append(kinds, "text")
			case ai.ImageContent:
				kinds = append(kinds, "image")
			}
		}
		if !reflect.DeepEqual(kinds, []string{"text", "image", "text", "text"}) {
			t.Fatalf("block kinds=%v", kinds)
		}
		if normalized[0] != (ai.TextContent{Text: "before"}) || normalized[3] != (ai.TextContent{Text: "after"}) {
			t.Fatalf("surrounding text=%#v, %#v", normalized[0], normalized[3])
		}
	})
}

func BenchmarkNormalizeOrderedToolResult(b *testing.B) {
	for name, image := range map[string]ai.ImageContent{"unchanged": {Data: toolResultTinyPNG, MimeType: "image/png"}, "resize": toolResultPNG(b, 2400, 100), "convert": toolResultBMP()} {
		b.Run(name, func(b *testing.B) {
			result := agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "before"}, image, ai.TextContent{Text: "after"}}}
			b.ReportAllocs()
			for b.Loop() {
				_ = NormalizeToolResultImages(result, true)
			}
		})
	}
}
