package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strings"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/imageprocessing"
)

// ─── Read Tool ────────────────────────────────────────────────────────────────

type readParams struct {
	Path   string   `json:"path"`
	Offset *float64 `json:"offset,omitempty"`
	Limit  *float64 `json:"limit,omitempty"`
}

// ReadTool reads file contents.
type ReadTool struct {
	CWD              string
	AutoResizeImages *bool
	ResizeOptions    *ai.ModelImageResizeOptions
	// Operations delegates the file reads; nil selects the local filesystem (upstream options.operations).
	Operations *ReadOperations
}

// ReadOperations is upstream's ReadOperations: pluggable file reads, for example over SSH.
// Ports packages/coding-agent/src/core/tools/read.ts.
type ReadOperations struct {
	// ReadFile reads the file contents.
	ReadFile func(absolutePath string) ([]byte, error)
	// Access fails when the file is not readable.
	Access func(absolutePath string) error
	// DetectImageMimeType returns the image MIME type, or "" for a non-image. A nil function treats every file as text.
	DetectImageMimeType func(absolutePath string) (string, error)
}

func (t *ReadTool) Name() string  { return "read" }
func (t *ReadTool) Label() string { return "" }

func (t *ReadTool) Schema() ai.ToolSchema {
	return toolSchemaWithParameters(ai.ToolSchema{
		Name:                "read",
		Description:         "Read the contents of a file. Supports text files and images (jpg, png, gif, webp, bmp). Images are sent as attachments. For text files, output is truncated to 2000 lines or 50KB (whichever is hit first). Use offset/limit for large files. When you need the full file, continue with offset until complete.",
		ConstrainedSampling: strictToolSampling(),
		PromptGuidelines: []string{
			"Use read to examine files instead of cat or sed.",
		},
	}, `{"type":"object","required":["path"],"properties":{
		"path":{"type":"string","description":"Path to the file to read (relative or absolute)"},
		"offset":{"type":"number","description":"Line number to start reading from (1-indexed)"},
		"limit":{"type":"number","description":"Maximum number of lines to read"}
	}}`)
}

func (t *ReadTool) ExecutionMode() agent.ToolExecutionMode { return agent.ToolModeParallel }

// readOutputSchema mirrors upstream readOutputSchema (read.ts): the result for programmatic callers such as codemode
// scripts, the text for text files and an image block for images that codemode's `image()` accepts. `note` is the text
// that goes with the image, such as resize hints. The key order is TypeBox's serialization.
const readOutputSchema = `{"anyOf":[{"type":"string"},{"type":"object","required":["type","data","mimeType","note"],"properties":{"type":{"type":"string","const":"image"},"data":{"type":"string"},"mimeType":{"type":"string"},"note":{"type":"string"}}}]}`

// OutputSchema is the schema of the result's structured content.
func (t *ReadTool) OutputSchema() json.RawMessage { return json.RawMessage(readOutputSchema) }

// readStructuredContent mirrors upstream toReadOutput: the image block and its note, or the text for text files and for
// images that could not be processed.
func readStructuredContent(content []ai.ToolResultMessageContent) json.RawMessage {
	var text string
	var image *ai.ImageContent
	haveText := false
	for _, block := range content {
		switch block := block.(type) {
		case ai.TextContent:
			if !haveText {
				text, haveText = block.Text, true
			}
		case ai.ImageContent:
			if image == nil {
				image = &block
			}
		}
	}
	var out any = text
	if image != nil {
		out = struct {
			Type     string `json:"type"`
			Data     string `json:"data"`
			MimeType string `json:"mimeType"`
			Note     string `json:"note"`
		}{"image", image.Data, image.MimeType, text}
	}
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(out)
	return json.RawMessage(bytes.TrimRight(buf.Bytes(), "\n"))
}

// Execute mirrors upstream read.ts execute: resolve the path, check it is
// readable, return a supported image as an attachment, and otherwise decode
// the text (invalid UTF-8 becomes U+FFFD, as Buffer.toString does) and apply
// offset, limit and head truncation.
func (t *ReadTool) Execute(ctx context.Context, _ string, rawParams json.RawMessage, _ agent.ToolUpdateCallback) (agent.AgentToolResult, error) {
	var p readParams
	if err := json.Unmarshal(rawParams, &p); err != nil {
		return agent.AgentToolResult{}, fmt.Errorf("read: invalid params: %w", err)
	}
	if ctx.Err() != nil {
		return agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "Operation aborted"}}, Details: map[string]any{}, IsError: true, Thrown: true}, nil
	}

	cwd, err := toolCWD(ctx, t.CWD)
	if err != nil {
		return agent.AgentToolResult{}, err
	}
	path, err := resolveReadPath(p.Path, cwd)
	if err != nil {
		return agent.AgentToolResult{}, err
	}
	access, readFile := checkReadable, os.ReadFile
	detect := func(path string, _ []byte) (string, error) {
		return imageprocessing.DetectSupportedImageMimeTypeFromFile(path)
	}
	if ops := t.Operations; ops != nil {
		access, readFile = ops.Access, ops.ReadFile
		detect = func(path string, _ []byte) (string, error) {
			if ops.DetectImageMimeType == nil {
				return "", nil
			}
			return ops.DetectImageMimeType(path)
		}
	}
	if err := access(path); err != nil {
		return agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: NodeFSError(err, "access", path)}}, Details: map[string]any{}, IsError: true, Thrown: true}, nil
	}
	// upstream: read.ts:129 a detection failure is a failed read, as the surrounding try/catch reports it.
	mime, err := detect(path, nil)
	if err != nil {
		return agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: err.Error()}}, Details: map[string]any{}, IsError: true, Thrown: true}, nil
	}
	data, err := readFile(path)
	if err != nil {
		return agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: NodeFSError(err, "read", "")}}, Details: map[string]any{}, IsError: true, Thrown: true}, nil
	}
	if ctx.Err() != nil {
		return agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "Operation aborted"}}, Details: map[string]any{}, IsError: true, Thrown: true}, nil
	}
	if mime != "" {
		result := t.readImage(ctx, data, mime)
		result.StructuredContent = readStructuredContent(result.Content)
		return result, nil
	}
	var decoder utf8StreamDecoder
	result, err := readTextResult(p, decoder.decode(data, false))
	if err != nil {
		return agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: err.Error()}}, Details: map[string]any{}, IsError: true, Thrown: true}, nil
	}
	result.StructuredContent = readStructuredContent(result.Content)
	return result, nil
}

// checkReadable mirrors upstream's access(path, R_OK).
func checkReadable(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	return f.Close()
}

// readTextResult mirrors the text branch of upstream read.ts execute.
func readTextResult(p readParams, textContent string) (agent.AgentToolResult, error) {
	allLines := strings.Split(textContent, "\n")
	totalFileLines := len(allLines)
	// offset is 1-indexed; JavaScript's slice truncates a fractional index.
	startLine := 0
	if p.Offset != nil && *p.Offset != 0 {
		// The comparison runs on the float, because an offset past the int range converts to a negative start.
		start := math.Max(0, *p.Offset-1)
		if start < float64(totalFileLines) {
			//portlint:allow numbers start is below totalFileLines (checked above), so the conversion stays in range
			startLine = int(start)
		} else {
			startLine = totalFileLines
		}
	}
	startLineDisplay := startLine + 1
	if startLine >= totalFileLines {
		return agent.AgentToolResult{}, fmt.Errorf("Offset %s is beyond end of file (%d lines total)", jsNumber(*p.Offset), totalFileLines)
	}

	var selected string
	userLimitedLines, hasUserLimit := 0, p.Limit != nil
	if hasUserLimit {
		// Math.min(startLine + limit, allLines.length), then slice(startLine,
		// endLine): a negative end counts from the end of the array.
		//portlint:allow numbers the minimum is at most the line count above; a limit below the int range converts to a negative end, which the slice rule below maps to an empty slice as JavaScript does
		endLine := int(math.Min(float64(startLine)+*p.Limit, float64(totalFileLines)))
		sliceEnd := endLine
		if sliceEnd < 0 {
			sliceEnd = max(totalFileLines+sliceEnd, 0)
		}
		selected = strings.Join(allLines[startLine:max(startLine, sliceEnd)], "\n")
		userLimitedLines = endLine - startLine
	} else {
		selected = strings.Join(allLines[startLine:], "\n")
	}
	tr := TruncateHead(selected, TruncationOptions{})

	var output string
	var truncation *TruncationResult
	switch {
	case tr.FirstLineExceedsLimit:
		firstLineSize := FormatSize(len(allLines[startLine]))
		output = fmt.Sprintf("[Line %d is %s, exceeds %s limit. Use bash: sed -n '%dp' %s | head -c %d]",
			startLineDisplay, firstLineSize, FormatSize(DefaultMaxBytes), startLineDisplay, p.Path, DefaultMaxBytes)
		truncation = &tr
	case tr.Truncated:
		endLineDisplay := startLineDisplay + tr.OutputLines - 1
		nextOffset := endLineDisplay + 1
		output = tr.Content
		if tr.TruncatedBy == "lines" {
			output += fmt.Sprintf("\n\n[Showing lines %d-%d of %d. Use offset=%d to continue.]",
				startLineDisplay, endLineDisplay, totalFileLines, nextOffset)
		} else {
			output += fmt.Sprintf("\n\n[Showing lines %d-%d of %d (%s limit). Use offset=%d to continue.]",
				startLineDisplay, endLineDisplay, totalFileLines, FormatSize(DefaultMaxBytes), nextOffset)
		}
		truncation = &tr
	case hasUserLimit && startLine+userLimitedLines < totalFileLines:
		remaining := totalFileLines - (startLine + userLimitedLines)
		nextOffset := startLine + userLimitedLines + 1
		output = fmt.Sprintf("%s\n\n[%d more lines in file. Use offset=%d to continue.]", tr.Content, remaining, nextOffset)
	default:
		output = tr.Content
	}
	result := agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: output}}}
	if truncation != nil {
		result.Details = &ReadDetails{Truncation: truncation}
	}
	return result, nil
}

var processReadImage = imageprocessing.ProcessImage

// readImage uses the execution model profile before the standalone fallback,
// matching createReadToolDefinition's ctx.model.inputLimits precedence.
func (t *ReadTool) readImage(ctx context.Context, data []byte, mime string) agent.AgentToolResult {
	env, _ := agent.ToolEnvironmentFrom(ctx)
	options := t.ResizeOptions
	if env.InputLimits != nil && env.InputLimits.Images != nil && env.InputLimits.Images.Resize != nil {
		options = env.InputLimits.Images.Resize
	}
	autoResize := t.AutoResizeImages == nil || *t.AutoResizeImages
	processed, processedMIME, hints, err := processReadImage(data, mime, autoResize, options)
	var text string
	if err != nil {
		text = fmt.Sprintf("Read image file [%s]\n%s", mime, err)
	} else {
		text = fmt.Sprintf("Read image file [%s]", processedMIME)
		if hints != "" {
			text += "\n" + hints
		}
	}
	if env.SupportsImages != nil && !*env.SupportsImages {
		text += "\n[Current model does not support images. The image will be omitted from this request.]"
	}
	result := agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: text}}}
	if err == nil {
		result.Content = append(result.Content, ai.ImageContent{MimeType: processedMIME, Data: base64Encode(processed)})
	}
	return result
}
