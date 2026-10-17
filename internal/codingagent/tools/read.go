package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/imageprocessing"
	"github.com/MichaelKinsy/PiG/internal/nodeerrno"
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
// images that could not be processed. It writes what a json.Encoder with SetEscapeHTML(false) writes, without the newline, into
// one buffer of the exact size.
func readStructuredContent(content []ai.ToolResultMessageContent) json.RawMessage {
	var text string
	var image ai.ImageContent
	haveText, haveImage := false, false
	for _, block := range content {
		switch block := block.(type) {
		case ai.TextContent:
			if !haveText {
				text, haveText = block.Text, true
			}
		case ai.ImageContent:
			if !haveImage {
				image, haveImage = block, true
			}
		}
	}
	if !haveImage {
		return appendEncodedString(make([]byte, 0, encodedStringLen(text)), text)
	}
	const imageMembers = `{"type":"image","data":,"mimeType":,"note":}`
	out := make([]byte, 0, len(imageMembers)+encodedStringLen(image.Data)+encodedStringLen(image.MimeType)+encodedStringLen(text))
	out = appendEncodedString(append(out, `{"type":"image","data":`...), image.Data)
	out = appendEncodedString(append(out, `,"mimeType":`...), image.MimeType)
	out = appendEncodedString(append(out, `,"note":`...), text)
	return append(out, '}')
}

// encodedASCIILen is the length encoding/json writes for each ASCII byte with HTML escaping off: 1 for the byte itself, 2 for a short
// escape and 6 for \u00XX.
var encodedASCIILen = func() (lengths [utf8.RuneSelf]uint8) {
	for c := range lengths {
		switch {
		case c == '"' || c == '\\' || c == '\b' || c == '\f' || c == '\n' || c == '\r' || c == '\t':
			lengths[c] = 2
		case c < 0x20:
			lengths[c] = 6
		default:
			lengths[c] = 1
		}
	}
	return lengths
}()

// encodedStringLen is len(appendEncodedString(nil, s)).
func encodedStringLen(s string) int {
	n := len(`""`)
	for i := 0; i < len(s); {
		if c := s[i]; c < utf8.RuneSelf {
			n += int(encodedASCIILen[c])
			i++
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == utf8.RuneError && size == 1:
			n += len(encodedInvalidUTF8)
		case r == '\u2028' || r == '\u2029':
			n += len(`\u2028`)
		default:
			n += size
		}
		i += size
	}
	return n
}

// appendEncodedString appends s as encoding/json writes a string with HTML escaping off: the short escapes, other controls as
// \u00XX, U+2028 and U+2029 escaped, and each byte of ill-formed UTF-8 as U+FFFD.
func appendEncodedString(dst []byte, s string) []byte {
	const hex = "0123456789abcdef"
	dst = append(dst, '"')
	start := 0
	for i := 0; i < len(s); {
		if c := s[i]; c < utf8.RuneSelf {
			if encodedASCIILen[c] == 1 {
				i++
				continue
			}
			dst = append(dst, s[start:i]...)
			switch c {
			case '"', '\\':
				dst = append(dst, '\\', c)
			case '\b':
				dst = append(dst, '\\', 'b')
			case '\f':
				dst = append(dst, '\\', 'f')
			case '\n':
				dst = append(dst, '\\', 'n')
			case '\r':
				dst = append(dst, '\\', 'r')
			case '\t':
				dst = append(dst, '\\', 't')
			default:
				dst = append(dst, '\\', 'u', '0', '0', hex[c>>4], hex[c&0xF])
			}
			i++
			start = i
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == utf8.RuneError && size == 1:
			dst = append(append(dst, s[start:i]...), encodedInvalidUTF8...)
			start = i + size
		case r == '\u2028' || r == '\u2029':
			dst = append(append(dst, s[start:i]...), '\\', 'u', '2', '0', '2', hex[r&0xF])
			start = i + size
		}
		i += size
	}
	dst = append(dst, s[start:]...)
	return append(dst, '"')
}

// RebuildStructuredContent returns the structured content the built-in tool named toolName derives from a result's content alone, and
// false for a tool whose structured content holds more than its content (bash's exit code and wall time) or that has none: read's is
// toReadOutput(content) (read.ts).
func RebuildStructuredContent(toolName string, content []ai.ToolResultMessageContent) (json.RawMessage, bool) {
	if toolName != "read" {
		return nil, false
	}
	return readStructuredContent(content), true
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
	var data []byte
	var mime string
	if ops := t.Operations; ops != nil {
		var failure string
		if data, mime, failure = readThroughOperations(ops, path); failure != "" {
			return agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: failure}}, Details: map[string]any{}, IsError: true, Thrown: true}, nil
		}
	} else {
		var failure string
		if data, mime, failure = readLocalFile(path); failure != "" {
			return agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: failure}}, Details: map[string]any{}, IsError: true, Thrown: true}, nil
		}
	}
	if ctx.Err() != nil {
		return agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "Operation aborted"}}, Details: map[string]any{}, IsError: true, Thrown: true}, nil
	}
	if mime != "" {
		result := t.readImage(ctx, data, mime)
		result.StructuredContent = readStructuredContent(result.Content)
		return result, nil
	}
	result, err := readTextResult(p, data)
	if err != nil {
		return agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: err.Error()}}, Details: map[string]any{}, IsError: true, Thrown: true}, nil
	}
	result.StructuredContent = readStructuredContent(result.Content)
	return result, nil
}

// readThroughOperations is upstream's access, image detection and read through pluggable operations, returning the failure text of
// the first step that fails.
func readThroughOperations(ops *ReadOperations, path string) ([]byte, string, string) {
	if err := ops.Access(path); err != nil {
		return nil, "", NodeFSError(err, "access", path)
	}
	mime := ""
	if ops.DetectImageMimeType != nil {
		var err error
		// upstream: read.ts:129 a detection failure is a failed read, as the surrounding try/catch reports it.
		if mime, err = ops.DetectImageMimeType(path); err != nil {
			return nil, "", nodeerrno.FromPathError(err).Error()
		}
	}
	data, err := ops.ReadFile(path)
	if err != nil {
		return nil, "", NodeFSError(err, "read", "")
	}
	return data, mime, ""
}

// readLocalFile is upstream's access(path, R_OK), image detection from the first ImageTypeSniffBytes bytes and readFile on one open
// file: opening is the access check, the first read is the detection's read and the rest completes readFile, each failure reported
// as that step reports it.
func readLocalFile(path string) ([]byte, string, string) {
	f, err := os.Open(path)
	if err != nil {
		return nil, "", NodeFSError(err, "access", path)
	}
	defer func() { _ = f.Close() }()
	size := 0
	//perf:ok fstat of the open fd once per read call, the one os.ReadFile makes to size its buffer; it replaces three opens and a stat
	if info, err := f.Stat(); err == nil && info.Mode().IsRegular() {
		size = int(info.Size())
	}
	// One byte past the size, so a file read whole sees EOF without growing the buffer (as os.ReadFile).
	data := make([]byte, 0, max(size, imageprocessing.ImageTypeSniffBytes)+1)
	n, err := io.ReadFull(f, data[:imageprocessing.ImageTypeSniffBytes])
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		// A failed read inside the detection is Node's fs error ("EISDIR: illegal operation on a directory, read").
		return nil, "", nodeerrno.FromPathError(err).Error()
	}
	data = data[:n]
	mime := imageprocessing.DetectSupportedImageMimeType(data)
	if n == imageprocessing.ImageTypeSniffBytes {
		for {
			if len(data) == cap(data) {
				data = append(data, 0)[:len(data)]
			}
			n, err := f.Read(data[len(data):cap(data)])
			data = data[:len(data)+n]
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return nil, "", NodeFSError(err, "read", "")
			}
		}
	}
	return data, mime, ""
}

// lineStart is the byte offset at which line `line` (0-based, split on "\n") of data starts, scanning forward from the start of line
// `from` at offset `at`.
func lineStart(data []byte, from, at, line int) int {
	for ; from < line; from++ {
		at += bytes.IndexByte(data[at:], '\n') + 1
	}
	return at
}

// decodeText is the file text Buffer.toString gives for data, a range of the file starting at a line start. Line boundaries survive
// decoding: 0x0A is never part of a multibyte sequence, and an invalid sequence before it becomes U+FFFD before the newline. The
// decoder keeps a BOM, so a range decodes as it reads in the whole file.
func decodeText(data []byte) string {
	var decoder utf8StreamDecoder
	return decoder.decode(data, false)
}

// readTextResult mirrors the text branch of upstream read.ts execute over the file's bytes. Upstream decodes the file and splits it into
// lines; this counts the lines and decodes only the selected ones.
func readTextResult(p readParams, data []byte) (agent.AgentToolResult, error) {
	totalFileLines := bytes.Count(data, []byte{'\n'}) + 1
	lineCount := float64(totalFileLines)
	// Line numbers stay JavaScript numbers as in read.ts:160-199: a fractional offset or limit shows up in the
	// continuation notices, and slice truncates it only when selecting lines.
	startLine := 0.0
	if p.Offset != nil && *p.Offset != 0 {
		startLine = math.Max(0, *p.Offset-1)
	}
	startLineDisplay := startLine + 1
	if startLine >= lineCount {
		return agent.AgentToolResult{}, fmt.Errorf("Offset %s is beyond end of file (%d lines total)", jsNumber(*p.Offset), totalFileLines)
	}
	//portlint:allow numbers 0 <= startLine < totalFileLines (checked above), so the truncated index is in range
	sliceStart := int(startLine)
	selectedStart := lineStart(data, 0, 0, sliceStart)
	// selectedText decodes lines [sliceStart, end) joined by "\n".
	selectedText := func(end int) string {
		if end <= sliceStart {
			return ""
		}
		if end >= totalFileLines {
			return decodeText(data[selectedStart:])
		}
		return decodeText(data[selectedStart : lineStart(data, sliceStart, selectedStart, end)-1])
	}

	var selected string
	userLimitedLines, hasUserLimit := 0.0, p.Limit != nil
	if hasUserLimit {
		endLine := math.Min(startLine+*p.Limit, lineCount)
		// slice truncates the end and counts a negative end from the end of the array.
		relativeEnd := math.Trunc(endLine)
		if relativeEnd < 0 {
			relativeEnd = math.Max(lineCount+relativeEnd, 0)
		}
		//portlint:allow numbers relativeEnd is clamped to [0, totalFileLines] above
		selected = selectedText(max(sliceStart, int(relativeEnd)))
		userLimitedLines = endLine - startLine
	} else {
		selected = selectedText(totalFileLines)
	}
	tr := TruncateHead(selected, TruncationOptions{})

	var output string
	var truncation *TruncationResult
	switch {
	case tr.FirstLineExceedsLimit:
		if startLine != float64(sliceStart) {
			// allLines[startLine] is undefined for a fractional index, so Buffer.byteLength throws (read.ts:181).
			return agent.AgentToolResult{}, errors.New(`The "string" argument must be of type string or an instance of Buffer or ArrayBuffer. Received undefined`)
		}
		firstLineSize := FormatSize(len(selectedText(sliceStart + 1)))
		line := itoa(sliceStart + 1)
		output = "[Line " + line + " is " + firstLineSize + ", exceeds " + FormatSize(DefaultMaxBytes) + " limit. Use bash: sed -n '" + line + "p' " +
			p.Path + " | head -c " + itoa(DefaultMaxBytes) + "]"
		truncation = &tr
	case tr.Truncated:
		endLineDisplay := startLineDisplay + float64(tr.OutputLines) - 1
		nextOffset := endLineDisplay + 1
		output = tr.Content
		if tr.TruncatedBy == "lines" {
			output += "\n\n[Showing lines " + jsNumber(startLineDisplay) + "-" + jsNumber(endLineDisplay) + " of " + itoa(totalFileLines) +
				". Use offset=" + jsNumber(nextOffset) + " to continue.]"
		} else {
			output += "\n\n[Showing lines " + jsNumber(startLineDisplay) + "-" + jsNumber(endLineDisplay) + " of " + itoa(totalFileLines) +
				" (" + FormatSize(DefaultMaxBytes) + " limit). Use offset=" + jsNumber(nextOffset) + " to continue.]"
		}
		truncation = &tr
	case hasUserLimit && startLine+userLimitedLines < lineCount:
		remaining := lineCount - (startLine + userLimitedLines)
		nextOffset := startLine + userLimitedLines + 1
		output = tr.Content + "\n\n[" + jsNumber(remaining) + " more lines in file. Use offset=" + jsNumber(nextOffset) + " to continue.]"
	default:
		output = tr.Content
	}
	result := agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: output}}}
	if truncation != nil {
		shareTruncatedContent(truncation, output)
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
