package sdk

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/extensions/sdk/kit"
)

// viewFrame is one frame the SDK wrote: kind is the envelope type, method the
// call or notify method, and body the call or notify args, the widget_push
// payload or the response result.
type viewFrame struct {
	kind   string
	method string
	body   json.RawMessage
	errMsg string
}

// viewHost runs an extension against a mock host that records every frame,
// answers host calls at once and answers ui.custom when the overlay closes.
type viewHost struct {
	t        *testing.T
	host     *mockHost
	register registerMsg
	frames   chan viewFrame
	done     chan error
	mu       sync.Mutex
}

func startViewHost(t *testing.T, state string, register func(ext *Extension)) *viewHost {
	t.Helper()
	host := newMockHost(t)
	ext := New("kit-view")
	register(ext)
	t.Setenv("PIG_EXT_SOCKET", host.sockPath)
	h := &viewHost{t: t, host: host, frames: make(chan viewFrame, 256), done: make(chan error, 1)}
	go func() { h.done <- ext.Run() }()
	host.accept(t)
	if env := host.readEnvelope(t); env.Register != nil {
		h.register = *env.Register
	}
	host.writeEnvelope(t, envelope{Type: msgReady, Ready: &readyMsg{Cwd: "/tmp", Width: 80, State: json.RawMessage(state)}})
	go h.record()
	t.Cleanup(func() {
		_ = h.send(envelope{Type: msgShutdown, Shutdown: &shutdownMsg{Reason: "done"}})
		select {
		case <-h.done:
		case <-time.After(5 * time.Second):
		}
		host.close()
	})
	return h
}

func (h *viewHost) record() {
	customCalls := map[string]string{}
	for {
		var hdr [4]byte
		if _, err := io.ReadFull(h.host.nc, hdr[:]); err != nil {
			return
		}
		data := make([]byte, binary.BigEndian.Uint32(hdr[:]))
		if _, err := io.ReadFull(h.host.nc, data); err != nil {
			return
		}
		var raw struct {
			Type string `json:"type"`
			ID   string `json:"id"`
			Call *struct {
				Method string          `json:"method"`
				Args   json.RawMessage `json:"args"`
			} `json:"call"`
			Notify *struct {
				Method string          `json:"method"`
				Args   json.RawMessage `json:"args"`
			} `json:"notify"`
			WidgetPush json.RawMessage `json:"widget_push"`
			Response   *struct {
				Result json.RawMessage `json:"result"`
				Error  *errorInfo      `json:"error"`
			} `json:"response"`
		}
		if json.Unmarshal(data, &raw) != nil {
			return
		}
		switch raw.Type {
		case msgCall:
			h.frames <- viewFrame{kind: msgCall, method: raw.Call.Method, body: raw.Call.Args}
			if raw.Call.Method == "ui.custom" {
				var args struct {
					Key string `json:"key"`
				}
				_ = json.Unmarshal(raw.Call.Args, &args)
				customCalls[args.Key] = raw.ID
				continue
			}
			_ = h.send(envelope{Type: msgCallResult, ID: raw.ID, CallResult: &callResultMsg{}})
		case msgNotify:
			h.frames <- viewFrame{kind: msgNotify, method: raw.Notify.Method, body: raw.Notify.Args}
			if raw.Notify.Method == "ui.custom.close" {
				var args struct {
					Key    string          `json:"key"`
					Result json.RawMessage `json:"result"`
				}
				_ = json.Unmarshal(raw.Notify.Args, &args)
				if id, ok := customCalls[args.Key]; ok {
					delete(customCalls, args.Key)
					result, _ := json.Marshal(map[string]any{"ok": true, "result": args.Result})
					_ = h.send(envelope{Type: msgCallResult, ID: id, CallResult: &callResultMsg{Result: result}})
				}
			}
		case msgWidgetPush:
			h.frames <- viewFrame{kind: msgWidgetPush, body: raw.WidgetPush}
		case msgResponse:
			frame := viewFrame{kind: msgResponse, body: raw.Response.Result}
			if raw.Response.Error != nil {
				frame.errMsg = raw.Response.Error.Message
			}
			h.frames <- frame
		}
	}
}

func (h *viewHost) send(env envelope) error {
	data, err := json.Marshal(env)
	if err != nil {
		return err
	}
	frame := binary.BigEndian.AppendUint32(nil, uint32(len(data)))
	h.mu.Lock()
	defer h.mu.Unlock()
	_, err = h.host.nc.Write(append(frame, data...))
	return err
}

func (h *viewHost) request(method, tool string, args any) {
	h.t.Helper()
	encoded, err := json.Marshal(args)
	if err != nil {
		h.t.Fatal(err)
	}
	if err := h.send(envelope{Type: msgRequest, ID: "req-" + tool, Request: &requestMsg{Method: method, Tool: tool, Args: encoded}}); err != nil {
		h.t.Fatal(err)
	}
}

func (h *viewHost) notify(method string, args any) {
	h.t.Helper()
	encoded, err := json.Marshal(args)
	if err != nil {
		h.t.Fatal(err)
	}
	if err := h.send(envelope{Type: msgNotify, Notify: &notifyMsg{Method: method, Args: encoded}}); err != nil {
		h.t.Fatal(err)
	}
}

// next returns the next frame of kind and method.
func (h *viewHost) next(kind, method string) viewFrame {
	h.t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case frame := <-h.frames:
			if frame.kind == kind && frame.method == method {
				return frame
			}
		case <-deadline:
			h.t.Fatalf("no %s %s frame within the deadline", kind, method)
		}
	}
}

// quiet fails when a frame of kind and method arrives within the window.
func (h *viewHost) quiet(kind, method string, window time.Duration) {
	h.t.Helper()
	deadline := time.After(window)
	for {
		select {
		case frame := <-h.frames:
			if frame.kind == kind && frame.method == method {
				h.t.Fatalf("unexpected %s %s frame: %s", kind, method, frame.body)
			}
		case <-deadline:
			return
		}
	}
}

func decodeObject(t *testing.T, raw json.RawMessage) map[string]json.RawMessage {
	t.Helper()
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	return object
}

type viewImages struct {
	Images []struct {
		Ref      string `json:"ref"`
		MimeType string `json:"mimeType"`
		Data     string `json:"data"`
	} `json:"images"`
}

func imagesOf(t *testing.T, view json.RawMessage) viewImages {
	t.Helper()
	var images viewImages
	if err := json.Unmarshal(view, &images); err != nil {
		t.Fatal(err)
	}
	return images
}

func testImageRef(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// kitPicker is a ViewComponent with a select list. It logs every input and
// event and closes on select with the log, as the conformance kit-probe does.
type kitPicker struct {
	mu         sync.Mutex
	log        []string
	label      string
	invalidate func()
	inputDelay time.Duration
	image      *kit.Image
}

func (p *kitPicker) View(int) kit.View {
	p.mu.Lock()
	defer p.mu.Unlock()
	items := []kit.SelectItem{{Value: "k0", Label: "Track 0"}, {Value: "k1", Label: "Track 1"}, {Value: "k2", Label: "Track 2"}}
	root := kit.NewContainer(kit.NewText(p.label, 1, 0), kit.NewSelectList("tracks", items, 3))
	if p.image != nil {
		root.AddChild(p.image)
	}
	return kit.View{Root: root, Focus: "tracks"}
}

func (p *kitPicker) HandleInput(data string) (RemoteComponentResult, error) {
	time.Sleep(p.inputDelay)
	p.mu.Lock()
	defer p.mu.Unlock()
	if data == "fail" {
		return RemoteComponentResult{}, errors.New("input failed")
	}
	p.log = append(p.log, "input:"+data)
	return RemoteComponentResult{}, nil
}

func (p *kitPicker) HandleViewEvent(event kit.Event) (RemoteComponentResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	value := ""
	if event.Item != nil {
		value = event.Item.Value
	}
	p.log = append(p.log, event.Type+":"+itoa(event.Index)+":"+value)
	if event.Type == kit.EventSelect {
		return RemoteComponentResult{Done: true, Value: strings.Join(p.log, ",")}, nil
	}
	if event.Type == kit.EventCancel {
		return RemoteComponentResult{}, errors.New("cancelled")
	}
	return RemoteComponentResult{}, nil
}

func (p *kitPicker) SetInvalidate(fn func()) {
	p.mu.Lock()
	p.invalidate = fn
	p.mu.Unlock()
}

func (p *kitPicker) relabel(label string) {
	p.mu.Lock()
	p.label = label
	invalidate := p.invalidate
	p.mu.Unlock()
	if invalidate != nil {
		invalidate()
	}
}

func itoa(n int) string {
	encoded, _ := json.Marshal(n)
	return string(encoded)
}

// openPicker registers command "go" that opens picker and reports its result
// as the command's notify.
func openPicker(picker *kitPicker) func(ext *Extension) {
	return func(ext *Extension) {
		ext.Command("go", "open", func(ctx Context, _ string) error {
			result, err := ctx.Custom(picker, nil)
			if err != nil {
				ctx.Notify("error="+err.Error(), "info")
				return nil
			}
			encoded, _ := json.Marshal(result)
			ctx.Notify("result="+string(encoded), "info")
			return nil
		})
	}
}

func selectEvent(key, node, typ string, index int, value string) map[string]any {
	return map[string]any{"key": key, "node": node, "type": typ, "index": index, "item": map[string]any{"value": value, "label": "Track", "description": "x"}}
}

func customFrame(t *testing.T, h *viewHost) (string, map[string]json.RawMessage) {
	t.Helper()
	frame := decodeObject(t, h.next(msgNotify, "ui.custom.render").body)
	var key string
	_ = json.Unmarshal(frame["key"], &key)
	return key, frame
}

func TestViewComponentFrameCarriesAViewAndNoLines(t *testing.T) {
	picker := &kitPicker{label: "Pick"}
	h := startViewHost(t, `{"hasUI":true}`, openPicker(picker))
	h.request("command", "go", nil)
	key, frame := customFrame(t, h)
	if _, ok := frame["lines"]; ok {
		t.Fatalf("view frame carries lines: %v", frame)
	}
	var seq uint64
	_ = json.Unmarshal(frame["seq"], &seq)
	if seq != 1 || key == "" {
		t.Fatalf("frame key %q seq %d", key, seq)
	}
	want := `{"root":{"kind":"container","children":[{"kind":"text","text":"Pick","paddingX":1,"paddingY":0},{"kind":"select-list","id":"tracks","items":[{"value":"k0","label":"Track 0"},{"value":"k1","label":"Track 1"},{"value":"k2","label":"Track 2"}],"maxVisible":3}]},"focus":"tracks"}`
	if string(frame["view"]) != want {
		t.Fatalf("view\n got %s\nwant %s", frame["view"], want)
	}
	h.notify(notifyUIViewEvent, selectEvent(key, "tracks", kit.EventSelect, 0, "k0"))
	h.next(msgNotify, "ui.custom.close")
}

func TestViewComponentDedupsAnIdenticalView(t *testing.T) {
	picker := &kitPicker{label: "One"}
	h := startViewHost(t, `{"hasUI":true}`, openPicker(picker))
	h.request("command", "go", nil)
	key, _ := customFrame(t, h)
	picker.relabel("One")
	h.quiet(msgNotify, "ui.custom.render", 150*time.Millisecond)
	picker.relabel("Two")
	_, frame := customFrame(t, h)
	var seq uint64
	_ = json.Unmarshal(frame["seq"], &seq)
	if seq != 2 || !strings.Contains(string(frame["view"]), `"text":"Two"`) {
		t.Fatalf("frame after a change = seq %d %s, want seq 2 with Two", seq, frame["view"])
	}
	h.notify(notifyUIViewEvent, selectEvent(key, "tracks", kit.EventSelect, 0, "k0"))
	h.next(msgNotify, "ui.custom.close")
}

func TestViewEventsShareTheInputQueueInArrivalOrder(t *testing.T) {
	// Input is slow, so an event dispatched apart from the input queue would
	// overtake the input sent before it.
	picker := &kitPicker{label: "Pick", inputDelay: 30 * time.Millisecond}
	h := startViewHost(t, `{"hasUI":true}`, openPicker(picker))
	h.request("command", "go", nil)
	key, _ := customFrame(t, h)
	h.notify(notifyUIViewEvent, selectEvent("custom-unknown", "tracks", kit.EventSelect, 0, "k0"))
	h.notify("ui.custom.input", map[string]any{"key": key, "data": "a"})
	h.notify(notifyUIViewEvent, selectEvent(key, "tracks", kit.EventSelectionChange, 1, "k1"))
	h.notify("ui.custom.input", map[string]any{"key": key, "data": "b"})
	h.notify(notifyUIViewEvent, map[string]any{"key": key, "node": "settings", "type": kit.EventChange, "index": 0, "id": "theme", "value": "light"})
	h.notify(notifyUIViewEvent, selectEvent(key, "tracks", kit.EventSelect, 2, "k2"))
	h.notify("ui.custom.input", map[string]any{"key": key, "data": "late"})
	closed := decodeObject(t, h.next(msgNotify, "ui.custom.close").body)
	var result string
	_ = json.Unmarshal(closed["result"], &result)
	if want := "input:a,selectionChange:1:k1,input:b,change:0:,select:2:k2"; result != want {
		t.Fatalf("close result = %q, want %q", result, want)
	}
	if _, ok := closed["error"]; ok {
		t.Fatalf("close carries an error: %v", closed)
	}
	var notify struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(h.next(msgCall, "ui.notify").body, &notify); err != nil {
		t.Fatal(err)
	}
	if want := `result="` + result + `"`; notify.Message != want {
		t.Fatalf("Custom returned %q, want %q", notify.Message, want)
	}
}

func TestViewEventErrorClosesWithTheError(t *testing.T) {
	picker := &kitPicker{label: "Pick"}
	h := startViewHost(t, `{"hasUI":true}`, openPicker(picker))
	h.request("command", "go", nil)
	key, _ := customFrame(t, h)
	h.notify(notifyUIViewEvent, map[string]any{"key": key, "node": "tracks", "type": kit.EventCancel, "index": 0})
	closed := decodeObject(t, h.next(msgNotify, "ui.custom.close").body)
	if string(closed["error"]) != `"cancelled"` {
		t.Fatalf("close = %v, want error cancelled", closed)
	}
}

// inputOnlyView is a ViewComponent without HandleViewEvent.
type inputOnlyView struct{}

func (inputOnlyView) View(int) kit.View { return kit.View{Root: kit.NewSpacer(1)} }
func (inputOnlyView) HandleInput(data string) (RemoteComponentResult, error) {
	return RemoteComponentResult{Done: true, Value: "input:" + data}, nil
}

func TestViewComponentWithoutEventHandlerIgnoresEvents(t *testing.T) {
	h := startViewHost(t, `{"hasUI":true}`, func(ext *Extension) {
		ext.Command("go", "open", func(ctx Context, _ string) error {
			_, err := ctx.Custom(inputOnlyView{}, nil)
			return err
		})
	})
	h.request("command", "go", nil)
	key, _ := customFrame(t, h)
	h.notify(notifyUIViewEvent, selectEvent(key, "tracks", kit.EventSelect, 0, "k0"))
	h.notify("ui.custom.input", map[string]any{"key": key, "data": "q"})
	closed := decodeObject(t, h.next(msgNotify, "ui.custom.close").body)
	if string(closed["result"]) != `"input:q"` {
		t.Fatalf("close = %v, want the input's result", closed)
	}
}

func TestViewImagesGoOnceAndAgainAfterEviction(t *testing.T) {
	data := []byte("cover art")
	ref := testImageRef(data)
	image := kit.NewImage(data, "image/png")
	view := kit.View{Root: kit.NewContainer(image)}
	h := startViewHost(t, `{"hasUI":true}`, func(ext *Extension) {
		ext.Command("go", "push", func(ctx Context, _ string) error {
			if err := ctx.SetWidget("cover", view); err != nil {
				return err
			}
			return ctx.SetWidget("cover", view)
		})
	})
	h.request("command", "go", nil)
	first := decodeObject(t, h.next(msgWidgetPush, "").body)
	if _, ok := first["lines"]; ok {
		t.Fatalf("widget view push carries lines: %v", first)
	}
	images := imagesOf(t, first["view"]).Images
	if len(images) != 1 || images[0].Ref != ref || images[0].MimeType != "image/png" || images[0].Data != "Y292ZXIgYXJ0" {
		t.Fatalf("first push images = %+v", images)
	}
	second := decodeObject(t, h.next(msgWidgetPush, "").body)
	if got := imagesOf(t, second["view"]).Images; len(got) != 0 {
		t.Fatalf("second push resent image bytes: %+v", got)
	}
	if !strings.Contains(string(second["view"]), `"ref":"`+ref+`"`) {
		t.Fatalf("second push lost the ref: %s", second["view"])
	}
	h.notify(notifyUIViewEvicted, map[string]any{"refs": []string{ref}})
	resent := decodeObject(t, h.next(msgWidgetPush, "").body)
	if got := imagesOf(t, resent["view"]).Images; len(got) != 1 || got[0].Ref != ref {
		t.Fatalf("push after eviction images = %+v, want the bytes again", got)
	}
}

func TestEvictedImageRerendersTheOverlay(t *testing.T) {
	data := []byte("overlay art")
	ref := testImageRef(data)
	picker := &kitPicker{label: "Pick", image: kit.NewImage(data, "image/jpeg")}
	h := startViewHost(t, `{"hasUI":true}`, openPicker(picker))
	h.request("command", "go", nil)
	key, frame := customFrame(t, h)
	if got := imagesOf(t, frame["view"]).Images; len(got) != 1 || got[0].Ref != ref {
		t.Fatalf("first frame images = %+v", got)
	}
	h.notify(notifyUIViewEvicted, map[string]any{"refs": []string{"unrelated"}})
	h.quiet(msgNotify, "ui.custom.render", 150*time.Millisecond)
	h.notify(notifyUIViewEvicted, map[string]any{"refs": []string{ref}})
	_, again := customFrame(t, h)
	var seq uint64
	_ = json.Unmarshal(again["seq"], &seq)
	if got := imagesOf(t, again["view"]).Images; seq != 2 || len(got) != 1 || got[0].Ref != ref {
		t.Fatalf("frame after eviction = seq %d images %+v", seq, got)
	}
	h.notify(notifyUIViewEvent, selectEvent(key, "tracks", kit.EventSelect, 0, "k0"))
	h.next(msgNotify, "ui.custom.close")
}

func TestFrontendOnlyAnnotationsFollowTheFrontendFlag(t *testing.T) {
	data := []byte("half-block cover")
	ref := testImageRef(data)
	lines := kit.NewLines([]string{"▀▀"})
	lines.Image = &kit.LinesImage{Data: data, MimeType: "image/png"}
	lines.Progress = &kit.Progress{Value: 3, Max: 10}
	lines.List = &kit.List{Items: []kit.ListItem{{Label: "Cover", Detail: "Artist"}}, Selected: 0}
	view := kit.View{Root: lines}
	for _, tc := range []struct {
		name     string
		state    string
		frontend bool
	}{
		{"no frontend", `{"hasUI":true}`, false},
		{"frontend", `{"hasUI":true,"frontend":true}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := startViewHost(t, tc.state, func(ext *Extension) {
				ext.Command("go", "push", func(ctx Context, _ string) error { return ctx.SetWidget("play", view) })
			})
			h.request("command", "go", nil)
			push := decodeObject(t, h.next(msgWidgetPush, "").body)
			got := string(push["view"])
			annotated := strings.Contains(got, `"image":{"ref":"`+ref+`"}`) && strings.Contains(got, `"progress":{"value":3,"max":10}`) &&
				strings.Contains(got, `"list":{"items":[{"label":"Cover","detail":"Artist"}],"selectedIndex":0}`)
			images := imagesOf(t, push["view"]).Images
			if tc.frontend && (!annotated || len(images) != 1) {
				t.Fatalf("frontend push = %s, want the annotations and the bytes", got)
			}
			if !tc.frontend && (strings.Contains(got, `"image"`) || strings.Contains(got, `"progress"`) || strings.Contains(got, `"list"`) || len(images) != 0) {
				t.Fatalf("push without a frontend = %s, want no annotations and no bytes", got)
			}
		})
	}
}

func TestWidgetHeaderAndFooterViews(t *testing.T) {
	view := kit.View{Root: kit.NewText("hi", 1, 0)}
	wantView := `{"root":{"kind":"text","text":"hi","paddingX":1,"paddingY":0}}`
	h := startViewHost(t, `{"hasUI":true}`, func(ext *Extension) {
		ext.Command("go", "set", func(ctx Context, _ string) error {
			if err := ctx.SetWidget("w", view, WidgetOptions{"placement": "belowEditor"}); err != nil {
				return err
			}
			if err := ctx.SetHeaderView(view); err != nil {
				return err
			}
			return ctx.SetFooterView(view)
		})
	})
	h.request("command", "go", nil)
	widget := decodeObject(t, h.next(msgCall, "ui.setWidget").body)
	if _, ok := widget["content"]; ok || string(widget["key"]) != `"w"` || string(widget["view"]) != wantView || string(widget["options"]) != `{"placement":"belowEditor"}` {
		t.Fatalf("ui.setWidget args = %v", widget)
	}
	for _, method := range []string{"ui.setHeader", "ui.setFooter"} {
		args := decodeObject(t, h.next(msgCall, method).body)
		if len(args) != 1 || string(args["view"]) != wantView {
			t.Fatalf("%s args = %v, want only the view", method, args)
		}
	}
}

func TestViewRenderersAnswerWithAView(t *testing.T) {
	view := kit.View{Root: kit.NewMarkdown("**done**", 0, 0)}
	wantView := `{"root":{"kind":"markdown","text":"**done**","paddingX":0,"paddingY":0}}`
	h := startViewHost(t, `{"hasUI":true}`, func(ext *Extension) {
		ext.Tool("probe", "probe", Schema{"type": "object"}, func(Context, map[string]any) (any, error) { return nil, nil })
		ext.SetToolRenderers("probe", ToolRenderers{
			Call:     func(Context, map[string]any, ToolRenderContext, int) ([]string, error) { return []string{"lines"}, nil },
			CallView: func(Context, map[string]any, ToolRenderContext, int) (kit.View, error) { return view, nil },
			ResultView: func(Context, ToolRenderResult, ToolRenderResultOptions, ToolRenderContext, int) (kit.View, error) {
				return kit.View{}, nil
			},
		})
		ext.MessageViewRenderer("note", func(Context, map[string]any, MessageRenderOptions, int) (kit.View, error) { return view, nil })
		ext.EntryViewRenderer("mark", func(Context, map[string]any, EntryRenderOptions, int) (kit.View, error) { return view, nil })
	})
	if len(h.register.Tools) != 1 || !h.register.Tools[0].RendersCall || !h.register.Tools[0].RendersResult {
		t.Fatalf("register tools = %+v, want call and result rendered", h.register.Tools)
	}
	if len(h.register.Renderers) != 1 || h.register.Renderers[0].CustomType != "note" || len(h.register.EntryRenderers) != 1 || h.register.EntryRenderers[0].CustomType != "mark" {
		t.Fatalf("register renderers = %+v / %+v", h.register.Renderers, h.register.EntryRenderers)
	}
	h.request("render_tool", "probe", map[string]any{"card": "c1", "phase": "call", "width": 40})
	call := h.next(msgResponse, "")
	if string(call.body) != `{"view":`+wantView+`}` {
		t.Fatalf("render_tool call = %s, want only the view", call.body)
	}
	h.request("render_tool", "probe", map[string]any{"card": "c1", "phase": "result", "width": 40})
	if result := h.next(msgResponse, ""); result.errMsg == "" {
		t.Fatalf("a view without a root answered %s, want an error", result.body)
	}
	h.request("render_message", "note", map[string]any{"message": map[string]any{}, "width": 40})
	if message := h.next(msgResponse, ""); string(message.body) != `{"view":`+wantView+`}` {
		t.Fatalf("render_message = %s", message.body)
	}
	h.request("render_entry", "mark", map[string]any{"entry": map[string]any{}, "width": 40})
	if entry := h.next(msgResponse, ""); string(entry.body) != `{"view":`+wantView+`}` {
		t.Fatalf("render_entry = %s", entry.body)
	}
}
