package tools

import (
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
		return agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "Operation aborted"}}, Details: map[string]any{}, IsError: true}, nil
	}

	cwd, err := toolCWD(ctx, t.CWD)
	if err != nil {
		return agent.AgentToolResult{}, err
	}
	path, err := resolveReadPath(p.Path, cwd)
	if err != nil {
		return agent.AgentToolResult{}, err
	}
	if err := checkReadable(path); err != nil {
		return agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: NodeFSError(err, "access", path)}}, Details: map[string]any{}, IsError: true}, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: NodeFSError(err, "read", "")}}, Details: map[string]any{}, IsError: true}, nil
	}
	if ctx.Err() != nil {
		return agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "Operation aborted"}}, Details: map[string]any{}, IsError: true}, nil
	}
	if mime := SupportedImageMime(data); mime != "" {
		return t.readImage(ctx, data, mime), nil
	}
	var decoder utf8StreamDecoder
	result, err := readTextResult(p, decoder.decode(data, false))
	if err != nil {
		return agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: err.Error()}}, Details: map[string]any{}, IsError: true}, nil
	}
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
		startLine = int(math.Max(0, *p.Offset-1))
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
	tr := TruncateHead(selected, DefaultMaxBytes, DefaultMaxLines)

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

func readToolWithSettings(cwd string, settings BashSettingsView) *ReadTool {
	tool := &ReadTool{CWD: cwd}
	if images, ok := settings.(interface{ GetImageAutoResize() bool }); ok {
		autoResize := images.GetImageAutoResize()
		tool.AutoResizeImages = &autoResize
	}
	return tool
}
