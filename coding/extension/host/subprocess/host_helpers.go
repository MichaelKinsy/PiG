package subprocess

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/codingagent/frontmatter"
	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
	"github.com/MichaelKinsy/PiG/internal/imageprocessing"
	"github.com/MichaelKinsy/PiG/internal/jsnumber"
	"github.com/MichaelKinsy/PiG/internal/nodebase64"
	"github.com/MichaelKinsy/PiG/internal/yaml12"
	"github.com/MichaelKinsy/PiG/tui"
)

// ParseFrontmatterJSON is Pi's parseFrontmatter over the host's parser: the JSON of the parsed header (see ParseFrontmatterResult) and the body. A
// malformed header is the error parseFrontmatter throws. The CallParseFrontmatter handler and the fused extensions' SDK hook share it.
func ParseFrontmatterJSON(content string) (frontmatterJSON []byte, body string, err error) {
	doc := frontmatter.ParseOrdered(content)
	if doc.Err != nil {
		return nil, "", doc.Err
	}
	var out strings.Builder
	if err := writeYAMLValue(&out, doc.Value); err != nil {
		return nil, "", err
	}
	return []byte(out.String()), doc.Body, nil
}

// ParseDateMs is `Date.parse(text)` through the host's port of V8's parser: the time value in milliseconds, false for NaN.
func ParseDateMs(text string) (float64, bool) {
	ms := ai.DateParse(text)
	return ms, !math.IsNaN(ms)
}

// handleParseFrontmatter serves CallParseFrontmatter.
func (h *Host) handleParseFrontmatter(call *CallPayload) (*CallResultPayload, error) {
	var args ParseFrontmatterArgs
	if err := json.Unmarshal(call.Args, &args); err != nil {
		return nil, fmt.Errorf("decode %s: %w", call.Method, err)
	}
	frontmatterJSON, body, err := ParseFrontmatterJSON(args.Content)
	if err != nil {
		return nil, err
	}
	// Written by hand: json.Marshal would rewrite the frontmatter's < > & and U+2028 as escapes, where JSON.stringify leaves them.
	data := append([]byte(`{"frontmatter":`), frontmatterJSON...)
	data = append(data, `,"body":`...)
	data = append(data, quoteJS(body)...)
	data = append(data, '}')
	return &CallResultPayload{Result: data}, nil
}

// HighlightCodeLines is pi-coding-agent's highlightCode over the host's highlight.js port, or getMarkdownTheme().highlightCode when markdown is set. palette is the
// theme to draw with (see HighlightCodeArgs.Theme); empty draws with the host's active theme. The CallHighlightCode handler and the fused extensions' SDK hook share it.
func HighlightCodeLines(code, lang string, markdown bool, palette []byte) (lines []string, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			lines, err = nil, fmt.Errorf("highlight code: %v", recovered)
		}
	}()
	// The languages are the ones the host has registered now, as Pi's highlightCode answers for the languages its process has loaded (the interactive mode loads the
	// rest in the background).
	theme := tui.ActiveTheme()
	if len(palette) > 0 {
		var wire struct {
			Name        string            `json:"name"`
			Foregrounds map[string]string `json:"foregrounds"`
			Backgrounds map[string]string `json:"backgrounds"`
			Mode        string            `json:"mode"`
		}
		if err := json.Unmarshal(palette, &wire); err != nil {
			return nil, fmt.Errorf("decode theme palette: %w", err)
		}
		mode := tui.TerminalColorModeTrueColor
		if wire.Mode == string(tui.TerminalColorMode256) {
			mode = tui.TerminalColorMode256
		}
		if theme, err = tui.NewThemeFromANSIPalette(wire.Name, wire.Foregrounds, wire.Backgrounds, mode); err != nil {
			return nil, err
		}
	}
	return tui.HighlightCodeFor(theme, code, lang, markdown), nil
}

// handleHighlightCode serves CallHighlightCode.
func (h *Host) handleHighlightCode(call *CallPayload) (*CallResultPayload, error) {
	var args HighlightCodeArgs
	if err := json.Unmarshal(call.Args, &args); err != nil {
		return nil, fmt.Errorf("decode %s: %w", call.Method, err)
	}
	lines, err := HighlightCodeLines(args.Code, args.Lang, args.Markdown, args.Theme)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(HighlightCodeResult{Lines: lines})
	if err != nil {
		return nil, err
	}
	return &CallResultPayload{Result: data}, nil
}

// themeLoader is the host's source of the palette CallLoadTheme answers: the theme initTheme(name) installs, which the system theme replaces for an absent or unknown name.
// The application installs it with SetThemeLoader; the theme registry is its state, not the Host's.
var themeLoader atomic.Pointer[func(name *string) any]

// SetThemeLoader installs the source of CallLoadTheme's palette.
func SetThemeLoader(load func(name *string) any) { themeLoader.Store(&load) }

// LoadThemeJSON is the palette of the theme initTheme(name) installs, encoded. The CallLoadTheme handler and the fused extensions' SDK hook share it.
func LoadThemeJSON(name *string) ([]byte, error) {
	load := themeLoader.Load()
	if load == nil || *load == nil {
		return nil, errors.New("the host has no theme to load")
	}
	palette, err := json.Marshal((*load)(name))
	if err != nil {
		return nil, fmt.Errorf("serialize theme: %w", err)
	}
	return palette, nil
}

// handleLoadTheme serves CallLoadTheme.
func (h *Host) handleLoadTheme(call *CallPayload) (*CallResultPayload, error) {
	var args LoadThemeArgs
	if err := json.Unmarshal(call.Args, &args); err != nil {
		return nil, fmt.Errorf("decode %s: %w", call.Method, err)
	}
	palette, err := LoadThemeJSON(args.Name)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(LoadThemeResult{Theme: palette})
	if err != nil {
		return nil, err
	}
	return &CallResultPayload{Result: data}, nil
}

// handleParseDate serves CallParseDate.
func (h *Host) handleParseDate(call *CallPayload) (*CallResultPayload, error) {
	var args ParseDateArgs
	if err := json.Unmarshal(call.Args, &args); err != nil {
		return nil, fmt.Errorf("decode %s: %w", call.Method, err)
	}
	var result ParseDateResult
	if ms, ok := ParseDateMs(args.Text); ok {
		result.Ms = &ms
	}
	data, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	return &CallResultPayload{Result: data}, nil
}

// writeYAMLValue is JSON.stringify of a value yaml12.ParseOrdered built: an object lists its array-index keys first, ascending, then the others in
// insertion order, and a non-finite number is null. A value only an explicit YAML tag builds (a set, a map, a date, a symbol, binary data) is an error here.
func writeYAMLValue(out *strings.Builder, value any) error {
	switch value := value.(type) {
	case nil:
		out.WriteString("null")
	case bool:
		out.WriteString(strconv.FormatBool(value))
	case float64:
		switch {
		case math.IsNaN(value) || math.IsInf(value, 0):
			out.WriteString("null")
		case value == 0:
			out.WriteString("0")
		default:
			out.WriteString(jsnumber.String(value))
		}
	case string:
		out.WriteString(quoteJS(value))
	case []any:
		out.WriteByte('[')
		for i, element := range value {
			if i > 0 {
				out.WriteByte(',')
			}
			if err := writeYAMLValue(out, element); err != nil {
				return err
			}
		}
		out.WriteByte(']')
	case map[string]any:
		// ParseOrdered builds only *yaml12.Object; the plain map is the `{}` Pi's `parsed ?? {}` supplies for a header that is absent or empty.
		if len(value) != 0 {
			return errors.New("frontmatter holds an object whose key order is unknown")
		}
		out.WriteString("{}")
	case *yaml12.Object:
		out.WriteByte('{')
		for i, key := range javaScriptKeyOrder(value.Keys) {
			if i > 0 {
				out.WriteByte(',')
			}
			out.WriteString(quoteJS(key))
			out.WriteByte(':')
			if err := writeYAMLValue(out, value.Values[key]); err != nil {
				return err
			}
		}
		out.WriteByte('}')
	default:
		return errors.New("frontmatter holds a value only a YAML tag builds (" + fmt.Sprintf("%T", value) + ")")
	}
	return nil
}

// javaScriptKeyOrder is the order Object.keys gives: the array-index keys ("0" up to 4294967294) ascending, then the other keys in insertion order.
func javaScriptKeyOrder(keys []string) []string {
	var indexes, others []string
	for _, key := range keys {
		if number, err := strconv.ParseUint(key, 10, 64); err == nil && number <= math.MaxUint32-1 && strconv.FormatUint(number, 10) == key {
			indexes = append(indexes, key)
		} else {
			others = append(others, key)
		}
	}
	slices.SortFunc(indexes, func(a, b string) int {
		if len(a) != len(b) {
			return len(a) - len(b)
		}
		return strings.Compare(a, b)
	})
	return append(indexes, others...)
}

// quoteJS is QuoteJSONString: a quote, a backslash and the control characters are escaped (\b \f \n \r \t short, the rest as lowercase \u00xx) and every other
// character, including < > & U+2028 and U+2029, is written as it is.
func quoteJS(text string) string {
	var out strings.Builder
	out.WriteByte('"')
	for _, c := range text {
		switch {
		case c == '"':
			out.WriteString(`\"`)
		case c == '\\':
			out.WriteString(`\\`)
		case c == '\b':
			out.WriteString(`\b`)
		case c == '\f':
			out.WriteString(`\f`)
		case c == '\n':
			out.WriteString(`\n`)
		case c == '\r':
			out.WriteString(`\r`)
		case c == '\t':
			out.WriteString(`\t`)
		case c < 0x20:
			fmt.Fprintf(&out, `\u%04x`, c)
		default:
			out.WriteRune(c)
		}
	}
	out.WriteByte('"')
	return out.String()
}

// clipboardCopier is the host's copy command, which the CLI installs; the helpers below serve Pi's copyToClipboard through it. It is process-wide because a fused extension
// calls the helper directly, with no Host in hand.
var clipboardCopier atomic.Pointer[func(string) error]

// SetClipboardCopier installs the host's copy function (Pi's copyToClipboard); nil removes it. CopyToClipboard fails while none is installed.
func SetClipboardCopier(copy func(text string) error) {
	if copy == nil {
		clipboardCopier.Store(nil)
		return
	}
	clipboardCopier.Store(&copy)
}

// CopyToClipboard is Pi's copyToClipboard through the host's copy function. The CallCopyToClipboard handler and the fused extensions' SDK hook share it.
func CopyToClipboard(text string) error {
	copier := clipboardCopier.Load()
	if copier == nil {
		return errors.New("clipboard: the host has no copy function")
	}
	return (*copier)(text)
}

// GenerateDiffString is Pi's generateDiffString over the host's port; contextLines nil is Pi's default of 4.
func GenerateDiffString(oldContent, newContent string, contextLines *int) GenerateDiffStringResult {
	var result tools.DiffStringResult
	if contextLines == nil {
		result = tools.GenerateDiffString(oldContent, newContent)
	} else {
		result = tools.GenerateDiffString(oldContent, newContent, *contextLines)
	}
	return GenerateDiffStringResult{Diff: result.Diff, FirstChangedLine: result.FirstChangedLine}
}

// GenerateUnifiedPatch is Pi's generateUnifiedPatch over the host's port; contextLines nil is Pi's default of 4.
func GenerateUnifiedPatch(path, oldContent, newContent string, contextLines *int) string {
	if contextLines == nil {
		return tools.GenerateUnifiedPatch(path, oldContent, newContent)
	}
	return tools.GenerateUnifiedPatch(path, oldContent, newContent, *contextLines)
}

// ConvertToPng is Pi's convertToPng over the host's image code: nil is Pi's null.
func ConvertToPng(data, mimeType string) *ImageResult {
	// convertToPng returns a PNG as it is and converts anything else, null when it does not decode (utils/image-convert.ts).
	convertedData, convertedMime := data, mimeType
	if mimeType != "image/png" {
		png := imageprocessing.ConvertImageBytesToPng(nodebase64.Decode(data))
		if png == nil {
			return &ImageResult{}
		}
		convertedData, convertedMime = base64.StdEncoding.EncodeToString(png), "image/png"
	}
	result := &ImageResult{Image: new(struct {
		Data           string `json:"data"`
		MimeType       string `json:"mimeType"`
		OriginalWidth  *int   `json:"originalWidth,omitempty"`
		OriginalHeight *int   `json:"originalHeight,omitempty"`
		Width          *int   `json:"width,omitempty"`
		Height         *int   `json:"height,omitempty"`
		WasResized     *bool  `json:"wasResized,omitempty"`
	})}
	result.Image.Data, result.Image.MimeType = convertedData, convertedMime
	return result
}

// ResizeImage is Pi's resizeImage over the host's image code: a nil Image is Pi's null.
func ResizeImage(args ResizeImageArgs) (*ImageResult, error) {
	input, err := base64.StdEncoding.DecodeString(args.Data)
	if err != nil {
		return nil, fmt.Errorf("decode image: %w", err)
	}
	var options *ai.ModelImageResizeOptions
	if args.Options != nil {
		options = &ai.ModelImageResizeOptions{MaxWidth: args.Options.MaxWidth, MaxHeight: args.Options.MaxHeight, MaxBytes: args.Options.MaxBytes, JPEGQuality: args.Options.JPEGQuality}
	}
	resized, err := imageprocessing.ResizeImage(input, args.MimeType, options)
	if err != nil {
		return &ImageResult{}, nil
	}
	result := &ImageResult{Image: new(struct {
		Data           string `json:"data"`
		MimeType       string `json:"mimeType"`
		OriginalWidth  *int   `json:"originalWidth,omitempty"`
		OriginalHeight *int   `json:"originalHeight,omitempty"`
		Width          *int   `json:"width,omitempty"`
		Height         *int   `json:"height,omitempty"`
		WasResized     *bool  `json:"wasResized,omitempty"`
	})}
	result.Image.Data, result.Image.MimeType = resized.Data, resized.MimeType
	result.Image.OriginalWidth, result.Image.OriginalHeight, result.Image.Width, result.Image.Height, result.Image.WasResized = &resized.OriginalWidth, &resized.OriginalHeight, &resized.Width, &resized.Height, &resized.WasResized
	return result, nil
}

func decodeArgsError(call *CallPayload, err error) error {
	return fmt.Errorf("decode %s: %w", call.Method, err)
}

func jsonResult(value any) (*CallResultPayload, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return &CallResultPayload{Result: data}, nil
}

// handleGenerateDiffString serves CallGenerateDiffString.
func (h *Host) handleGenerateDiffString(call *CallPayload) (*CallResultPayload, error) {
	var args GenerateDiffStringArgs
	if err := json.Unmarshal(call.Args, &args); err != nil {
		return nil, decodeArgsError(call, err)
	}
	return jsonResult(GenerateDiffString(args.OldContent, args.NewContent, args.ContextLines))
}

// handleGenerateUnifiedPatch serves CallGenerateUnifiedPatch.
func (h *Host) handleGenerateUnifiedPatch(call *CallPayload) (*CallResultPayload, error) {
	var args GenerateUnifiedPatchArgs
	if err := json.Unmarshal(call.Args, &args); err != nil {
		return nil, decodeArgsError(call, err)
	}
	return jsonResult(GenerateUnifiedPatchResult{Patch: GenerateUnifiedPatch(args.Path, args.OldContent, args.NewContent, args.ContextLines)})
}

// handleCopyToClipboard serves CallCopyToClipboard.
func (h *Host) handleCopyToClipboard(call *CallPayload) (*CallResultPayload, error) {
	var args CopyToClipboardArgs
	if err := json.Unmarshal(call.Args, &args); err != nil {
		return nil, decodeArgsError(call, err)
	}
	if err := CopyToClipboard(args.Text); err != nil {
		return nil, err
	}
	return &CallResultPayload{Result: []byte(`{}`)}, nil
}

// handleConvertToPng serves CallConvertToPng.
func (h *Host) handleConvertToPng(call *CallPayload) (*CallResultPayload, error) {
	var args ConvertToPngArgs
	if err := json.Unmarshal(call.Args, &args); err != nil {
		return nil, decodeArgsError(call, err)
	}
	return jsonResult(ConvertToPng(args.Data, args.MimeType))
}

// handleResizeImage serves CallResizeImage.
func (h *Host) handleResizeImage(call *CallPayload) (*CallResultPayload, error) {
	var args ResizeImageArgs
	if err := json.Unmarshal(call.Args, &args); err != nil {
		return nil, decodeArgsError(call, err)
	}
	result, err := ResizeImage(args)
	if err != nil {
		return nil, err
	}
	return jsonResult(result)
}
