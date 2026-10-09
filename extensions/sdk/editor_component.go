package sdk

import (
	"fmt"
	"slices"
	"sync"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// EditorComponent is the editor an extension installs with
// [Context.SetEditorComponent]. Pi's factory returns a CustomEditor subclass: the
// host's keys reach its handleInput, its render output is the editor on screen, and its
// `super` calls reach the default editor. Embed the [Editor] the factory receives to inherit
// the default behaviour of every method and override the ones that differ.
//
// Every method runs on one goroutine per installed editor, in the order the host produced the
// events. After each event the SDK renders the component and sends the frame when it changed.
type EditorComponent interface {
	// HandleInput receives one key. The default editor runs the extension shortcuts, then the
	// app actions, then its own editing.
	HandleInput(data string) error
	// HandleMouse receives a left click on the editor rows.
	HandleMouse(event EditorMouseEvent) error
	// Render returns the editor rows for width.
	Render(width int) ([]string, error)
	// SetText, InsertTextAtCursor and AddToHistory are the calls the host makes on its editor.
	SetText(text string) error
	InsertTextAtCursor(text string) error
	AddToHistory(text string) error
}

// EditorFactory builds the editor component around the host's default editor.
type EditorFactory func(base *Editor) EditorComponent

// EditorMouseEvent is pi-tui's TuiMouseEvent with the row relative to the editor's first row.
type EditorMouseEvent struct {
	Type       string `json:"type"`
	Button     string `json:"button"`
	X          int    `json:"x"`
	Y          int    `json:"y"`
	ScreenX    int    `json:"screenX"`
	ScreenY    int    `json:"screenY"`
	Width      int    `json:"width"`
	Height     int    `json:"height"`
	Shift      bool   `json:"shift"`
	Alt        bool   `json:"alt"`
	Ctrl       bool   `json:"ctrl"`
	ClickCount int    `json:"clickCount,omitempty"`
}

// EditorCursor is the position of the editor's cursor: a logical line and a UTF-16 column.
type EditorCursor struct {
	Line int `json:"line"`
	Col  int `json:"col"`
}

// Editor is the host's default editor, the `super` of an [EditorComponent]. Every method is a
// call to the host's editor and is valid once the factory has returned.
type Editor struct {
	ext *Extension
	key string
}

func (e *Editor) call(op string, args map[string]any) (json.RawMessage, error) {
	result, err := e.ext.conn.call("ui.editor.base", map[string]any{"key": e.key, "op": op, "args": args})
	if err := callResultError(result, err); err != nil {
		return nil, err
	}
	if result == nil {
		return nil, nil
	}
	return result.Result, nil
}

func (e *Editor) field(op string, args map[string]any, dst any) error {
	raw, err := e.call(op, args)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		return fmt.Errorf("editor %s reply: %w", op, err)
	}
	return nil
}

// HandleInput is the default editor's handleInput.
func (e *Editor) HandleInput(data string) error {
	_, err := e.call("handleInput", map[string]any{"data": data})
	return err
}

// HandleMouse is the default editor's handleMouse.
func (e *Editor) HandleMouse(event EditorMouseEvent) error {
	_, err := e.call("handleMouse", map[string]any{"event": event})
	return err
}

// Render is the default editor's render.
func (e *Editor) Render(width int) ([]string, error) {
	var reply struct {
		Lines []string `json:"lines"`
	}
	err := e.field("render", map[string]any{"width": width}, &reply)
	return reply.Lines, err
}

// SetText is the default editor's setText.
func (e *Editor) SetText(text string) error {
	_, err := e.call("setText", map[string]any{"text": text})
	return err
}

// InsertTextAtCursor is the default editor's insertTextAtCursor.
func (e *Editor) InsertTextAtCursor(text string) error {
	_, err := e.call("insertTextAtCursor", map[string]any{"text": text})
	return err
}

// AddToHistory is the default editor's addToHistory.
func (e *Editor) AddToHistory(text string) error {
	_, err := e.call("addToHistory", map[string]any{"text": text})
	return err
}

// GetText is the default editor's getText.
func (e *Editor) GetText() (string, error) {
	var reply struct {
		Text string `json:"text"`
	}
	err := e.field("getText", nil, &reply)
	return reply.Text, err
}

// GetExpandedText is the default editor's getExpandedText: the text with paste markers expanded.
func (e *Editor) GetExpandedText() (string, error) {
	var reply struct {
		Text string `json:"text"`
	}
	err := e.field("getExpandedText", nil, &reply)
	return reply.Text, err
}

// GetLines is the default editor's getLines.
func (e *Editor) GetLines() ([]string, error) {
	var reply struct {
		Lines []string `json:"lines"`
	}
	err := e.field("getLines", nil, &reply)
	return reply.Lines, err
}

// GetCursor is the default editor's getCursor.
func (e *Editor) GetCursor() (EditorCursor, error) {
	var cursor EditorCursor
	err := e.field("getCursor", nil, &cursor)
	return cursor, err
}

// IsShowingAutocomplete is the default editor's isShowingAutocomplete.
func (e *Editor) IsShowingAutocomplete() (bool, error) {
	var reply struct {
		Value bool `json:"value"`
	}
	err := e.field("isShowingAutocomplete", nil, &reply)
	return reply.Value, err
}

// SetPaddingX is the default editor's setPaddingX.
func (e *Editor) SetPaddingX(padding int) error {
	_, err := e.call("setPaddingX", map[string]any{"n": padding})
	return err
}

// SetAutocompleteMaxVisible is the default editor's setAutocompleteMaxVisible.
func (e *Editor) SetAutocompleteMaxVisible(maxVisible int) error {
	_, err := e.call("setAutocompleteMaxVisible", map[string]any{"n": maxVisible})
	return err
}

// editorSession is one installed editor component and the goroutine that runs it.
type editorSession struct {
	ext       *Extension
	key       string
	component EditorComponent

	mu        sync.Mutex
	queue     []func()
	wake      chan struct{}
	done      chan struct{}
	closeOnce sync.Once

	frameSeq  uint64
	lastLines []string
	lastWidth int
	rendered  bool
}

func (s *editorSession) post(task func()) {
	s.mu.Lock()
	s.queue = append(s.queue, task)
	s.mu.Unlock()
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *editorSession) close() {
	s.closeOnce.Do(func() { close(s.done) })
}

func (s *editorSession) run() {
	for {
		s.mu.Lock()
		var task func()
		if len(s.queue) > 0 {
			task = s.queue[0]
			s.queue = s.queue[1:]
		}
		s.mu.Unlock()
		if task == nil {
			select {
			case <-s.wake:
				continue
			case <-s.done:
				return
			}
		}
		select {
		case <-s.done:
			return
		default:
		}
		task()
	}
}

func (s *editorSession) fail(what string, err error) {
	if err == nil {
		return
	}
	_ = s.ext.conn.notify("ui.notify", map[string]any{"message": fmt.Sprintf("editor %s failed: %v", what, err), "level": "error"})
}

// renderNow renders the component and sends the frame when it changed, as Pi's TUI renders the editor after input.
func (s *editorSession) renderNow() {
	s.ext.mu.RLock()
	width := s.ext.width
	s.ext.mu.RUnlock()
	if width <= 0 {
		width = 80
	}
	lines, err := s.component.Render(width)
	if err != nil {
		s.fail("render", err)
		return
	}
	if lines == nil {
		lines = []string{}
	}
	if s.rendered && width == s.lastWidth && slices.Equal(lines, s.lastLines) {
		return
	}
	s.rendered, s.lastLines, s.lastWidth = true, lines, width
	s.frameSeq++
	_ = s.ext.conn.notify("ui.editor.render", map[string]any{"key": s.key, "lines": lines, "width": width, "seq": s.frameSeq})
}

// handleEditorNotify routes the host's editor notifies to the installed component; it reports whether method was one.
func (e *Extension) handleEditorNotify(method string, args json.RawMessage) bool {
	switch method {
	case "ui.editor.input", "ui.editor.mouse", "ui.editor.setText", "ui.editor.insertText", "ui.editor.addToHistory", "ui.editor.configure", "ui.editor.closed":
	default:
		return false
	}
	var payload struct {
		Key   string           `json:"key"`
		Data  string           `json:"data"`
		Text  string           `json:"text"`
		Event EditorMouseEvent `json:"event"`
	}
	if json.Unmarshal(args, &payload) != nil {
		return true
	}
	e.editorMu.Lock()
	s := e.editor
	e.editorMu.Unlock()
	if s == nil || s.key != payload.Key {
		return true
	}
	switch method {
	case "ui.editor.input":
		s.post(func() {
			s.fail("handleInput", s.component.HandleInput(payload.Data))
			s.renderNow()
			_ = e.conn.notify("ui.editor.inputDone", map[string]any{"key": s.key})
		})
	case "ui.editor.mouse":
		s.post(func() { s.fail("handleMouse", s.component.HandleMouse(payload.Event)); s.renderNow() })
	case "ui.editor.setText":
		s.post(func() { s.fail("setText", s.component.SetText(payload.Text)); s.renderNow() })
	case "ui.editor.insertText":
		s.post(func() { s.fail("insertTextAtCursor", s.component.InsertTextAtCursor(payload.Text)); s.renderNow() })
	case "ui.editor.addToHistory":
		s.post(func() { s.fail("addToHistory", s.component.AddToHistory(payload.Text)); s.renderNow() })
	case "ui.editor.configure":
		s.post(s.renderNow)
	case "ui.editor.closed":
		e.detachEditor(s)
	}
	return true
}

func (e *Extension) detachEditor(s *editorSession) {
	e.editorMu.Lock()
	if e.editor == s {
		e.editor = nil
	}
	e.editorMu.Unlock()
	s.close()
}

// refreshEditor renders the installed editor again after the terminal changed.
func (e *Extension) refreshEditor() {
	e.editorMu.Lock()
	s := e.editor
	e.editorMu.Unlock()
	if s != nil {
		s.post(s.renderNow)
	}
}

// EditorStatusEmbedder is implemented by an [EditorComponent] that draws the working, compaction, summarization and
// retry status in its top border, as Pi's CustomEditor does with embedWorkingStatus: true. The host then renders the
// status into the rows that [Editor.Render] returns instead of showing it above the editor.
type EditorStatusEmbedder interface {
	EmbedWorkingStatus() bool
}

// installEditor installs the component the factory builds around the host's default editor. The host learns of the
// editor before the factory runs, so the factory may already call the [Editor] it receives: such a call waits until
// the host's editor is ready.
func (e *Extension) installEditor(factory EditorFactory) (err error) {
	key := fmt.Sprintf("editor-%d", e.editorSeq.Add(1))
	s := &editorSession{ext: e, key: key, wake: make(chan struct{}, 1), done: make(chan struct{})}
	e.editorMu.Lock()
	previous := e.editor
	e.editor = s
	e.editorMu.Unlock()
	if previous != nil {
		previous.close()
	}
	if err := e.conn.notify("ui.editor.install", map[string]any{"key": key, "delegated": true}); err != nil {
		e.detachEditor(s)
		return err
	}
	built := false
	defer func() {
		if !built {
			e.detachEditor(s)
			_ = e.conn.notify("ui.editor.clear", map[string]any{"key": key})
		}
	}()
	component := factory(&Editor{ext: e, key: key})
	if component == nil {
		return fmt.Errorf("editor factory returned no component")
	}
	s.component = component
	built = true
	go s.run()
	if embedder, ok := component.(EditorStatusEmbedder); ok && embedder.EmbedWorkingStatus() {
		return e.conn.notify("ui.editor.options", map[string]any{"key": key, "embedWorkingStatus": true})
	}
	return nil
}
