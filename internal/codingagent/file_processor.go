package codingagent

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/text"
)

// ProcessedCLIArgs is the upstream-shaped result of processing CLI @file
// arguments: text placeholders plus image attachments.
type ProcessedCLIArgs struct {
	Text   string
	Images []ai.ImageContent
}

// ProcessCLIFileArguments expands CLI @file arguments into the upstream
// initial-message payload. Text files are wrapped as
// <file name="/abs/path">\ncontent\n</file>\n. Image files are attached as
// ai.ImageContent blocks and represented in the text stream as either an empty
// <file> tag (no resize needed) or a dimension note when resized.
func ProcessCLIFileArguments(fileArgs []string, cwd string) (ProcessedCLIArgs, error) {
	if len(fileArgs) == 0 {
		return ProcessedCLIArgs{}, nil
	}
	var out strings.Builder
	var images []ai.ImageContent
	for _, arg := range fileArgs {
		absPath := arg
		if !filepath.IsAbs(absPath) {
			absPath = filepath.Join(cwd, arg)
		}
		absPath = filepath.Clean(absPath)
		info, err := os.Stat(absPath)
		if err != nil {
			return ProcessedCLIArgs{}, fmt.Errorf("File not found: %s", absPath)
		}
		if info.Size() == 0 {
			continue
		}
		content, err := os.ReadFile(absPath)
		if err != nil {
			return ProcessedCLIArgs{}, fmt.Errorf("could not read file %s: %w", absPath, err)
		}
		if mime := DetectSupportedImageMimeType(content); mime != "" {
			resized, resizedMime, note, err := PrepareCLIImageAttachment(content, mime)
			if err != nil {
				return ProcessedCLIArgs{}, fmt.Errorf("could not process image %s: %w", absPath, err)
			}
			images = append(images, ai.ImageContent{
				MimeType: resizedMime,
				Data:     base64.StdEncoding.EncodeToString(resized),
			})
			out.WriteString(`<file name="`)
			out.WriteString(absPath)
			out.WriteString(`">`)
			out.WriteString(note)
			out.WriteString("</file>\n")
			continue
		}
		content = text.StripBomBytes(content)
		out.WriteString(`<file name="`)
		out.WriteString(absPath)
		out.WriteString(`">`)
		out.WriteByte('\n')
		out.Write(content)
		if len(content) == 0 || content[len(content)-1] != '\n' {
			out.WriteByte('\n')
		}
		out.WriteString("</file>\n")
	}
	return ProcessedCLIArgs{Text: out.String(), Images: images}, nil
}
