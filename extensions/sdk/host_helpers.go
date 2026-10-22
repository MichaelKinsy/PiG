package sdk

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net"
	"os"
	"slices"
	"sync"
)

// Host calls for Pi's pure helpers that need the host's parsers: parseFrontmatter (the host's YAML parser, the one that reads skills and prompt templates)
// and Date.parse (the host's port of V8's). They read no session state, so the host serves them before an extension registers, and a helper called while
// the factory runs (before Run) dials the host itself: the first call opens the connection and Run later reuses it. A call from a handler uses the
// handler's own connection (Context.ParseFrontmatter, Context.ParseDate).
const (
	callParseFrontmatter = "frontmatter.parse"
	callParseDate        = "date.parse"
	// The host's ports of Pi's generateDiffString, generateUnifiedPatch, copyToClipboard, convertToPng and resizeImage.
	callGenerateDiffString   = "diff.string"
	callGenerateUnifiedPatch = "diff.patch"
	callCopyToClipboard      = "clipboard.copy"
	callConvertToPng         = "image.convertToPng"
	callResizeImage          = "image.resize"
	// callHighlightCode is highlightCode, and callLoadTheme the theme initTheme installs: the host's highlight.js port and its theme registry. Like the two above they read no
	// session state, so the host serves them before an extension registers.
	callHighlightCode = "highlight.code"
	callLoadTheme     = "theme.load"
)

// ParsedFrontmatter is the result of ParseFrontmatter: Pi's `{ frontmatter, body }`. Frontmatter is the JSON of `parse(yaml) ?? {}`, with each object's keys
// in the order a JavaScript object enumerates them (array-index keys first, ascending, then insertion order), which a Go map would lose; it is a JSON object
// for the usual mapping header and the array, string, number or boolean for any other YAML document.
type ParsedFrontmatter struct {
	Frontmatter json.RawMessage
	Body        string
}

// BuiltinToolPeer is the part of a fused extension a built-in tool the host runs for it reaches back into: the functions of the call's options (Pi's
// `operations` and `spawnHook`) and its onUpdate, which stay in the extension. See [HostHelpers.BuiltinToolExecute].
type BuiltinToolPeer struct {
	// Callback runs one function of the call's options under handle and returns the JSON of its result, nil when it has none. An error is the callback's throw.
	Callback func(ctx context.Context, handle, member string, args []byte) ([]byte, error)
	// Update delivers the JSON of one partial result to the call's onUpdate; an error is onUpdate's throw.
	Update func(ctx context.Context, executeID string, result []byte) error
	// Exec is operations.exec of the options' shell operations under handle: it streams output to onData in order and returns Pi's `{ exitCode }`, or the error
	// exec rejects with. The command ends with ctx.
	Exec func(ctx context.Context, handle, command, cwd string, timeout *float64, env map[string]string, onData func([]byte)) (*int, error)
}

// HostHelpers are the host's parsers and file mutation queue for a fused extension, which runs in the host's own process and has no connection before Run. The code
// that builds the fused binary installs them with SetHostHelpers before it runs any factory.
type HostHelpers struct {
	ParseFrontmatter func(content string) (ParsedFrontmatter, error)
	// ParseDate is Date.parse: the time value in milliseconds, false for NaN.
	ParseDate func(text string) (float64, bool)
	// GenerateDiffString and GenerateUnifiedPatch are the host's ports of Pi's generateDiffString and generateUnifiedPatch; a nil contextLines is Pi's default of 4.
	GenerateDiffString   func(oldContent, newContent string, contextLines *int) DiffString
	GenerateUnifiedPatch func(path, oldContent, newContent string, contextLines *int) string
	// CopyToClipboard is the host's copy function.
	CopyToClipboard func(text string) error
	// ConvertToPng is the host's convertToPng: false is Pi's null.
	ConvertToPng func(data, mimeType string) (ConvertedImage, bool)
	// ResizeImage is the host's resizeImage: false is Pi's null.
	ResizeImage func(input []byte, mimeType string, options *ImageResizeOptions) (ResizedImage, bool, error)
	// BuiltinToolDefinition answers builtinTool.definition: the JSON of its arguments in, the JSON of its reply out.
	BuiltinToolDefinition func(args []byte) ([]byte, error)
	// LocalExec answers builtinTool.localExec in the host's own process: the JSON of its arguments in (the executeId is unused), the JSON of Pi's `{ exitCode }` out.
	// The command ends with ctx. Each output chunk goes to onData in order before LocalExec returns, and an error from onData ends the command and is LocalExec's.
	// The call has no Context, so a connection of its own would be any running extension's, which ends when that extension does, whichever made the call.
	LocalExec func(ctx context.Context, args []byte, onData func(data []byte) error) ([]byte, error)
	// BuiltinToolExecute answers builtinTool.execute of a call without a Context in the host's own process: the JSON of its arguments in (the executeId and
	// context are unused), the JSON of the tool's result out, or the error the tool throws. The tool ends with ctx. The functions of the call's options and its
	// onUpdate are in this process, which peer reaches. The call has no Context, so a connection of its own would be any running extension's, which ends when that
	// extension does, whichever made the call.
	BuiltinToolExecute func(ctx context.Context, args []byte, peer BuiltinToolPeer) ([]byte, error)
	// HighlightCode is highlightCode (markdown false) or getMarkdownTheme().highlightCode (markdown true), drawn with the palette of the theme initTheme installed, or with the
	// host's active theme when palette is empty.
	HighlightCode func(code, lang string, markdown bool, palette []byte) ([]string, error)
	// LoadTheme is the palette of the theme initTheme(name) installs; a nil name is initTheme(undefined).
	LoadTheme func(name *string) ([]byte, error)
	// WithFileMutationQueue runs fn in the host's file mutation queue for the absolute path (the host's WithFileMutationQueue) and returns the resolve error or fn's error.
	WithFileMutationQueue func(filePath string, fn func() error) error
}

var helpers struct {
	mu   sync.Mutex
	hook *HostHelpers
	// dialled holds the connections helpers opened before Run, by socket path; Run takes its own.
	dialled map[string]*conn
	// factory is the connection of the fused extension whose factory ServeFactory is running; the pi.events calls of that factory use it.
	factory *conn
	// running are the connections of the extensions that are running (a packed cell runs several), with the socket path each serves (empty for one that has none), for a helper called from
	// code that has no Context and no socket of its own: the first that is still running answers.
	running []runningConn
}

// SetHostHelpers installs the host's parsers for a fused extension; a nil value removes them.
func SetHostHelpers(h *HostHelpers) {
	helpers.mu.Lock()
	defer helpers.mu.Unlock()
	helpers.hook = h
}

// ParseFrontmatter is Pi's parseFrontmatter (utils/frontmatter.ts, exported from index.ts), answered by the host's parser. A header the YAML parser
// refuses is the error Pi throws. It may be called before Run: the first call connects to the host, and Run reuses the connection.
func ParseFrontmatter(content string) (ParsedFrontmatter, error) {
	if hook := installedHelpers(); hook != nil && hook.ParseFrontmatter != nil {
		return hook.ParseFrontmatter(content)
	}
	c, err := helperConnection()
	if err != nil {
		return ParsedFrontmatter{}, err
	}
	result, err := c.call(callParseFrontmatter, map[string]string{"content": content})
	return decodeFrontmatter(result, err)
}

// StripFrontmatter is Pi's stripFrontmatter: the body of ParseFrontmatter.
func StripFrontmatter(content string) (string, error) {
	parsed, err := ParseFrontmatter(content)
	return parsed.Body, err
}

// ParseDate is `Date.parse(text)` (`new Date(text).getTime()` for a string), V8's parser answered by the host: the time value in milliseconds, and false when it
// is NaN. It may be called before Run, like ParseFrontmatter.
func ParseDate(text string) (float64, bool, error) {
	if hook := installedHelpers(); hook != nil && hook.ParseDate != nil {
		ms, ok := hook.ParseDate(text)
		return ms, ok, nil
	}
	c, err := helperConnection()
	if err != nil {
		return 0, false, err
	}
	result, err := c.call(callParseDate, map[string]string{"text": text})
	return decodeDate(result, err)
}

// ParseFrontmatter is ParseFrontmatter over the handler's own connection.
func (c Context) ParseFrontmatter(content string) (ParsedFrontmatter, error) {
	result, err := c.callHost(callParseFrontmatter, map[string]string{"content": content})
	return decodeFrontmatter(result, err)
}

// ParseDate is ParseDate over the handler's own connection.
func (c Context) ParseDate(text string) (float64, bool, error) {
	result, err := c.callHost(callParseDate, map[string]string{"text": text})
	return decodeDate(result, err)
}

func decodeFrontmatter(result *callResultMsg, err error) (ParsedFrontmatter, error) {
	if err != nil {
		return ParsedFrontmatter{}, err
	}
	var reply struct {
		Frontmatter json.RawMessage `json:"frontmatter"`
		Body        string          `json:"body"`
	}
	if err := json.Unmarshal(result.Result, &reply); err != nil {
		return ParsedFrontmatter{}, fmt.Errorf("decode parseFrontmatter: %w", err)
	}
	return ParsedFrontmatter{Frontmatter: reply.Frontmatter, Body: reply.Body}, nil
}

func decodeDate(result *callResultMsg, err error) (float64, bool, error) {
	if err != nil {
		return 0, false, err
	}
	var reply struct {
		Ms *float64 `json:"ms"`
	}
	if err := json.Unmarshal(result.Result, &reply); err != nil {
		return 0, false, fmt.Errorf("decode Date.parse: %w", err)
	}
	if reply.Ms == nil {
		return 0, false, nil
	}
	return *reply.Ms, true, nil
}

func installedHelpers() *HostHelpers {
	helpers.mu.Lock()
	defer helpers.mu.Unlock()
	return helpers.hook
}

// runningConn is the connection of a running extension and the socket it serves.
type runningConn struct {
	c    *conn
	path string
	// ext is the running extension, for a helper that reaches the host through it.
	ext *Extension
}

// helperConnection is the connection a helper called outside a handler uses: the running extension's, the one an earlier helper opened to this socket, or a
// new one, which Run reuses. A runner that names a socket (PIG_EXT_SOCKET, set to the member's own while its factory runs) means the extension that socket
// belongs to, so a helper called by one member's factory never rides another member's connection, which the host serves only after it has accepted this
// member's: it uses the running extension on that socket, and dials the socket when this member has not connected. With no socket named, the first extension
// that is still running answers.
func helperConnection() (*conn, error) {
	helpers.mu.Lock()
	defer helpers.mu.Unlock()
	path := helperSocketPath()
	for _, running := range helpers.running {
		if path == "" || running.path == "" || running.path == path {
			return running.c, nil
		}
	}
	if path == "" {
		return nil, errors.New("not connected to the host: PIG_EXT_SOCKET is not set, so this extension was not launched by pig")
	}
	return dialLocked(path)
}

// dialLocked is the connection helpers opened to path, or a new one, which Run reuses. helpers.mu is held.
func dialLocked(path string) (*conn, error) {
	if c := helpers.dialled[path]; c != nil {
		return c, nil
	}
	nc, err := net.Dial("unix", path)
	if err != nil {
		return nil, fmt.Errorf("connect to host: %w", err)
	}
	c := newConn(nc)
	c.start()
	if helpers.dialled == nil {
		helpers.dialled = map[string]*conn{}
	}
	helpers.dialled[path] = c
	return c, nil
}

// factoryConnection is the connection the pi.events calls of a factory use before Run: the one ServeFactory opened for a fused extension, or the one dialled to this extension's socket, which Run reuses. It is never a running extension's: a bus call attributes the listeners and emits it carries to the realm of the connection it rides, and a running extension's is another extension's.
func factoryConnection() (*conn, error) {
	helpers.mu.Lock()
	defer helpers.mu.Unlock()
	if c := helpers.factory; c != nil {
		return c, nil
	}
	path := helperSocketPath()
	if path == "" {
		return nil, errNoFactoryHost
	}
	return dialLocked(path)
}

// fusedFactoryMu serializes the factories ServeFactory runs: helpers.factory is the connection of the one running.
var fusedFactoryMu sync.Mutex

// ServeFactory runs factory and serves the extension it returns over nc, as factory().RunWithConn(nc) does, with one difference: nc is the factory's own connection while the factory runs, so its pi.events calls reach the host at once, as a subprocess extension's do over the socket it dials. A fused Piglet Binary serves its extensions with it. The connection is closed when the serve loop ends.
func ServeFactory(nc net.Conn, factory Factory) error {
	started := newConn(nc)
	started.start()
	ext, err := factoryOnConn(started, factory)
	if err != nil {
		_ = nc.Close()
		return err
	}
	return ext.runWithConn(nc, started, "")
}

func factoryOnConn(c *conn, factory Factory) (*Extension, error) {
	fusedFactoryMu.Lock()
	defer fusedFactoryMu.Unlock()
	helpers.mu.Lock()
	helpers.factory = c
	helpers.mu.Unlock()
	defer func() {
		helpers.mu.Lock()
		helpers.factory = nil
		helpers.mu.Unlock()
	}()
	ext := factory()
	if ext == nil {
		return nil, errors.New("factory returned nil")
	}
	return ext, nil
}

// runningConnectionsExcept are the connections of the running extensions other than c. It never dials: it is for a helper that must reach the host after the
// connection it started on has gone.
func runningConnectionsExcept(c *conn) []*conn {
	helpers.mu.Lock()
	defer helpers.mu.Unlock()
	var others []*conn
	for _, running := range helpers.running {
		if running.c != c {
			others = append(others, running.c)
		}
	}
	return others
}

// helperSocketPath is the socket a helper dials: PIG_EXT_SOCKET, which a packed cell's runner sets to the member's socket while the member's factory runs.
func helperSocketPath() string { return os.Getenv("PIG_EXT_SOCKET") }

// takeHelperConn hands Run the connection a helper opened to sockPath, once.
func takeHelperConn(sockPath string) *conn {
	helpers.mu.Lock()
	defer helpers.mu.Unlock()
	c := helpers.dialled[sockPath]
	delete(helpers.dialled, sockPath)
	return c
}

// adoptHelperConn makes the connection of a running extension the one helpers use when they run outside a factory and a Context, so such a helper does not
// dial a socket the host already accepted.
func adoptHelperConn(c *conn, path string, ext *Extension) {
	helpers.mu.Lock()
	defer helpers.mu.Unlock()
	helpers.running = append(helpers.running, runningConn{c: c, path: path, ext: ext})
}

// releaseHelperConn forgets c when its extension stopped running.
func releaseHelperConn(c *conn) {
	helpers.mu.Lock()
	defer helpers.mu.Unlock()
	helpers.running = slices.DeleteFunc(helpers.running, func(running runningConn) bool { return running.c == c })
}

// DiffString is the result of GenerateDiffString: Pi's `{ diff, firstChangedLine }`. FirstChangedLine is nil when the texts do not differ (undefined in Pi).
type DiffString struct {
	Diff             string
	FirstChangedLine *int
}

// ConvertedImage is the result of ConvertToPng: Pi's `{ data, mimeType }`.
type ConvertedImage struct {
	Data     string
	MimeType string
}

// ImageResizeOptions is Pi's ImageResizeOptions (utils/image-resize-core.ts). A zero field takes Pi's default.
type ImageResizeOptions struct {
	MaxWidth    int `json:"maxWidth,omitempty"`
	MaxHeight   int `json:"maxHeight,omitempty"`
	MaxBytes    int `json:"maxBytes,omitempty"`
	JPEGQuality int `json:"jpegQuality,omitempty"`
}

func contextLinesArg(contextLines []int) *int {
	if len(contextLines) == 0 {
		return nil
	}
	return &contextLines[0]
}

// helperCall sends a pure-helper call over the connection a helper outside a handler uses.
func helperCall(method string, args any) (*callResultMsg, error) {
	c, err := helperConnection()
	if err != nil {
		return nil, err
	}
	return c.call(method, args)
}

func decodeResult(result *callResultMsg, err error, into any) error {
	if err != nil {
		return err
	}
	if err := json.Unmarshal(result.Result, into); err != nil {
		return fmt.Errorf("decode host reply: %w", err)
	}
	return nil
}

// GenerateDiffString is Pi's generateDiffString (core/tools/edit-diff.ts, exported from index.ts): the line-numbered display diff of two texts, with the context around each
// change cut to contextLines (default 4) lines, and the first changed line of the new text. The host's byte-exact port answers. It may be called before Run, like ParseFrontmatter.
func GenerateDiffString(oldContent, newContent string, contextLines ...int) (DiffString, error) {
	if hook := installedHelpers(); hook != nil && hook.GenerateDiffString != nil {
		return hook.GenerateDiffString(oldContent, newContent, contextLinesArg(contextLines)), nil
	}
	return decodeDiffString(helperCall(callGenerateDiffString, diffStringArgs(oldContent, newContent, contextLines)))
}

// GenerateDiffString is GenerateDiffString over the handler's own connection.
func (c Context) GenerateDiffString(oldContent, newContent string, contextLines ...int) (DiffString, error) {
	return decodeDiffString(c.callHost(callGenerateDiffString, diffStringArgs(oldContent, newContent, contextLines)))
}

func diffStringArgs(oldContent, newContent string, contextLines []int) map[string]any {
	args := map[string]any{"oldContent": oldContent, "newContent": newContent}
	if len(contextLines) > 0 {
		args["contextLines"] = contextLines[0]
	}
	return args
}

func decodeDiffString(result *callResultMsg, err error) (DiffString, error) {
	var reply struct {
		Diff             string `json:"diff"`
		FirstChangedLine *int   `json:"firstChangedLine"`
	}
	if err := decodeResult(result, err, &reply); err != nil {
		return DiffString{}, err
	}
	return DiffString{Diff: reply.Diff, FirstChangedLine: reply.FirstChangedLine}, nil
}

// GenerateUnifiedPatch is Pi's generateUnifiedPatch (core/tools/edit-diff.ts, exported from index.ts): a standard unified patch of two texts for one path, with contextLines
// (default 4) lines of context around each change. The host's port of jsdiff answers. It may be called before Run.
func GenerateUnifiedPatch(path, oldContent, newContent string, contextLines ...int) (string, error) {
	if hook := installedHelpers(); hook != nil && hook.GenerateUnifiedPatch != nil {
		return hook.GenerateUnifiedPatch(path, oldContent, newContent, contextLinesArg(contextLines)), nil
	}
	return decodePatch(helperCall(callGenerateUnifiedPatch, patchArgs(path, oldContent, newContent, contextLines)))
}

// GenerateUnifiedPatch is GenerateUnifiedPatch over the handler's own connection.
func (c Context) GenerateUnifiedPatch(path, oldContent, newContent string, contextLines ...int) (string, error) {
	return decodePatch(c.callHost(callGenerateUnifiedPatch, patchArgs(path, oldContent, newContent, contextLines)))
}

func patchArgs(path, oldContent, newContent string, contextLines []int) map[string]any {
	args := diffStringArgs(oldContent, newContent, contextLines)
	args["path"] = path
	return args
}

func decodePatch(result *callResultMsg, err error) (string, error) {
	var reply struct {
		Patch string `json:"patch"`
	}
	if err := decodeResult(result, err, &reply); err != nil {
		return "", err
	}
	return reply.Patch, nil
}

// CopyToClipboard is Pi's copyToClipboard (utils/clipboard.ts, exported from index.ts): the host copies text with the strategy its own copy commands use, and a failed copy is
// the error. It may be called before Run.
func CopyToClipboard(text string) error {
	if hook := installedHelpers(); hook != nil && hook.CopyToClipboard != nil {
		return hook.CopyToClipboard(text)
	}
	result, err := helperCall(callCopyToClipboard, map[string]string{"text": text})
	return decodeResult(result, err, &struct{}{})
}

// CopyToClipboard is CopyToClipboard over the handler's own connection.
func (c Context) CopyToClipboard(text string) error {
	result, err := c.callHost(callCopyToClipboard, map[string]string{"text": text})
	return decodeResult(result, err, &struct{}{})
}

type imageReply struct {
	Image *struct {
		Data           string   `json:"data"`
		MimeType       string   `json:"mimeType"`
		OriginalWidth  *float64 `json:"originalWidth"`
		OriginalHeight *float64 `json:"originalHeight"`
		Width          *float64 `json:"width"`
		Height         *float64 `json:"height"`
		WasResized     bool     `json:"wasResized"`
	} `json:"image"`
}

// ConvertToPng is Pi's convertToPng (utils/image-convert.ts, exported from index.ts): base64 image data as base64 PNG data, which a PNG already is. It returns nil, as Pi
// returns null, when the data does not decode. It may be called before Run.
func ConvertToPng(base64Data, mimeType string) (*ConvertedImage, error) {
	if hook := installedHelpers(); hook != nil && hook.ConvertToPng != nil {
		converted, ok := hook.ConvertToPng(base64Data, mimeType)
		if !ok {
			return nil, nil
		}
		return &converted, nil
	}
	return decodeConverted(helperCall(callConvertToPng, map[string]string{"data": base64Data, "mimeType": mimeType}))
}

// ConvertToPng is ConvertToPng over the handler's own connection.
func (c Context) ConvertToPng(base64Data, mimeType string) (*ConvertedImage, error) {
	return decodeConverted(c.callHost(callConvertToPng, map[string]string{"data": base64Data, "mimeType": mimeType}))
}

func decodeConverted(result *callResultMsg, err error) (*ConvertedImage, error) {
	var reply imageReply
	if err := decodeResult(result, err, &reply); err != nil {
		return nil, err
	}
	if reply.Image == nil {
		return nil, nil
	}
	return &ConvertedImage{Data: reply.Image.Data, MimeType: reply.Image.MimeType}, nil
}

// ResizeImage is Pi's resizeImage (utils/image-resize.ts, exported from index.ts): the image fitted within the option limits (2000 pixels on a side, 4.5 MiB encoded unless
// options say otherwise), as the host's image code does it. It returns nil, as Pi returns null, when the image cannot be made to fit. An image already within every limit comes
// back as it was. It may be called before Run.
func ResizeImage(inputBytes []byte, mimeType string, options *ImageResizeOptions) (*ResizedImage, error) {
	if hook := installedHelpers(); hook != nil && hook.ResizeImage != nil {
		resized, ok, err := hook.ResizeImage(inputBytes, mimeType, options)
		if err != nil || !ok {
			return nil, err
		}
		return &resized, nil
	}
	return decodeResized(helperCall(callResizeImage, resizeArgs(inputBytes, mimeType, options)))
}

// ResizeImage is ResizeImage over the handler's own connection.
func (c Context) ResizeImage(inputBytes []byte, mimeType string, options *ImageResizeOptions) (*ResizedImage, error) {
	return decodeResized(c.callHost(callResizeImage, resizeArgs(inputBytes, mimeType, options)))
}

func resizeArgs(inputBytes []byte, mimeType string, options *ImageResizeOptions) map[string]any {
	args := map[string]any{"data": base64.StdEncoding.EncodeToString(inputBytes), "mimeType": mimeType}
	if options != nil {
		args["options"] = options
	}
	return args
}

func decodeResized(result *callResultMsg, err error) (*ResizedImage, error) {
	var reply imageReply
	if err := decodeResult(result, err, &reply); err != nil {
		return nil, err
	}
	if reply.Image == nil {
		return nil, nil
	}
	image := reply.Image
	number := func(value *float64) float64 {
		if value == nil {
			return math.NaN()
		}
		return *value
	}
	return &ResizedImage{Data: image.Data, MimeType: image.MimeType, OriginalWidth: number(image.OriginalWidth), OriginalHeight: number(image.OriginalHeight), Width: number(image.Width), Height: number(image.Height), WasResized: image.WasResized}, nil
}

// highlightCodeLines asks the host for highlight.js's lines of code in lang, drawn with the installed theme (see [InitTheme]) or the host's active one.
func highlightCodeLines(code, lang string, markdown bool) ([]string, error) {
	palette := installedThemePalette()
	if hook := installedHelpers(); hook != nil && hook.HighlightCode != nil {
		return hook.HighlightCode(code, lang, markdown, palette)
	}
	c, err := helperConnection()
	if err != nil {
		return nil, err
	}
	args := map[string]any{"code": code, "lang": lang}
	if markdown {
		args["markdown"] = true
	}
	if len(palette) > 0 {
		args["theme"] = palette
	}
	result, err := c.call(callHighlightCode, args)
	if err := callResultError(result, err); err != nil {
		return nil, err
	}
	var reply struct {
		Lines []string `json:"lines"`
	}
	if err := json.Unmarshal(result.Result, &reply); err != nil {
		return nil, fmt.Errorf("decode highlightCode: %w", err)
	}
	return reply.Lines, nil
}

// loadTheme asks the host for the palette of the theme initTheme(name) installs.
func loadTheme(name *string) (json.RawMessage, error) {
	if hook := installedHelpers(); hook != nil && hook.LoadTheme != nil {
		return hook.LoadTheme(name)
	}
	c, err := helperConnection()
	if err != nil {
		return nil, err
	}
	result, err := c.call(callLoadTheme, map[string]any{"name": name})
	if err := callResultError(result, err); err != nil {
		return nil, err
	}
	var reply struct {
		Theme json.RawMessage `json:"theme"`
	}
	if err := json.Unmarshal(result.Result, &reply); err != nil {
		return nil, fmt.Errorf("decode initTheme: %w", err)
	}
	return reply.Theme, nil
}
