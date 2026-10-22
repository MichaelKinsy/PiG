package sdk

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"sync"
	"testing"
	"time"
)

// Pi 0.87.1 installs a footer or header as a component factory and the TUI
// calls render(width) every frame (interactive-mode.ts:2418-2480), so rows are
// always laid out for the current width. A subprocess extension cannot be
// called every frame, so the SDK sends the width each set of rows was laid out
// for and the host never paints rows for another width (public issue #104).

type widthSurfaceFrame struct {
	Lines []string `json:"lines"`
	Width int      `json:"width"`
	Clear bool     `json:"clear"`
}

// widthSurfaceHost runs an extension with one command "go" against a mock host,
// answers every host call, and records the UI calls in order.
type widthSurfaceHost struct {
	t     *testing.T
	host  *mockHost
	calls chan callMsg
	done  chan error
	mu    sync.Mutex // one frame at a time: the recorder replies while the test writes
}

func startWidthSurfaceHost(t *testing.T, register func(ext *Extension)) *widthSurfaceHost {
	t.Helper()
	return startWidthSurfaceHostIn(t, "tui", true, register)
}

// startWidthSurfaceHostIn is startWidthSurfaceHost with the mode and hasUI the host reports: Pi runs a footer or header factory only in the TUI.
func startWidthSurfaceHostIn(t *testing.T, mode string, hasUI bool, register func(ext *Extension)) *widthSurfaceHost {
	t.Helper()
	host := newMockHost(t)
	ext := New("surface-width")
	register(ext)
	t.Setenv("PIG_EXT_SOCKET", host.sockPath)
	s := &widthSurfaceHost{t: t, host: host, calls: make(chan callMsg, 64), done: make(chan error, 1)}
	go func() { s.done <- ext.Run() }()
	host.accept(t)
	_ = host.readEnvelope(t)
	host.writeEnvelope(t, envelope{Type: msgReady, Ready: &readyMsg{Cwd: "/tmp", Width: 80, Mode: mode, State: json.RawMessage(fmt.Sprintf(`{"hasUI":%t}`, hasUI))}})
	go func() {
		for {
			env, ok := s.read()
			if !ok {
				return
			}
			if env.Type == msgCall && env.Call != nil {
				s.calls <- *env.Call
				_ = s.send(envelope{Type: msgCallResult, ID: env.ID, CallResult: &callResultMsg{}})
			}
			// A widget_push has no reply; it is recorded as a call named
			// widget_push whose arguments are the payload.
			if env.Type == msgWidgetPush && env.WidgetPush != nil {
				args, _ := json.Marshal(env.WidgetPush)
				s.calls <- callMsg{Method: msgWidgetPush, Args: args}
			}
		}
	}()
	t.Cleanup(func() {
		_ = s.send(envelope{Type: msgShutdown, Shutdown: &shutdownMsg{Reason: "done"}})
		select {
		case <-s.done:
		case <-time.After(5 * time.Second):
		}
		host.close()
	})
	return s
}

func (s *widthSurfaceHost) send(env envelope) error {
	data, err := json.Marshal(env)
	if err != nil {
		return err
	}
	frame := binary.BigEndian.AppendUint32(nil, uint32(len(data)))
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err = s.host.nc.Write(append(frame, data...))
	return err
}

func (s *widthSurfaceHost) mustSend(env envelope) {
	s.t.Helper()
	if err := s.send(env); err != nil {
		s.t.Fatal(err)
	}
}

// read returns the next envelope, or false once the connection closes. It
// never calls t.Fatal because it runs on the recorder goroutine.
func (s *widthSurfaceHost) read() (envelope, bool) {
	for {
		var hdr [4]byte
		if _, err := io.ReadFull(s.host.nc, hdr[:]); err != nil {
			return envelope{}, false
		}
		data := make([]byte, binary.BigEndian.Uint32(hdr[:]))
		if _, err := io.ReadFull(s.host.nc, data); err != nil {
			return envelope{}, false
		}
		var env envelope
		if err := json.Unmarshal(data, &env); err != nil {
			return envelope{}, false
		}
		if env.Type != msgRequestState {
			return env, true
		}
	}
}

func (s *widthSurfaceHost) command() {
	s.t.Helper()
	s.mustSend(envelope{Type: msgRequest, ID: "req-go", Request: &requestMsg{Method: "command", Tool: "go"}})
}

func (s *widthSurfaceHost) width(width int) {
	s.t.Helper()
	args, err := json.Marshal(map[string]int{"width": width})
	if err != nil {
		s.t.Fatal(err)
	}
	s.mustSend(envelope{Type: msgNotify, Notify: &notifyMsg{Method: "width_change", Args: args}})
}

// next returns the arguments of the next host call named method.
func (s *widthSurfaceHost) next(method string) json.RawMessage {
	s.t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case call := <-s.calls:
			if call.Method == method {
				return call.Args
			}
		case <-deadline:
			s.t.Fatalf("no %s call within the deadline", method)
		}
	}
}

func (s *widthSurfaceHost) frame(method string) widthSurfaceFrame {
	s.t.Helper()
	var frame widthSurfaceFrame
	if err := json.Unmarshal(s.next(method), &frame); err != nil {
		s.t.Fatal(err)
	}
	return frame
}

func (s *widthSurfaceHost) quiet(method string) {
	s.t.Helper()
	select {
	case call := <-s.calls:
		if call.Method == method {
			s.t.Fatalf("unexpected %s call: %s", method, call.Args)
		}
	case <-time.After(300 * time.Millisecond):
	}
}

func TestSetFooterAndHeaderTagRowsWithTheSDKWidth(t *testing.T) {
	for _, tc := range []struct {
		name, method string
		set          func(ctx Context, lines []string) error
	}{
		{"footer", "ui.setFooter", func(ctx Context, lines []string) error { return ctx.SetFooter(lines) }},
		{"header", "ui.setHeader", func(ctx Context, lines []string) error { return ctx.SetHeader(lines) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := startWidthSurfaceHost(t, func(ext *Extension) {
				ext.Command("go", "push rows", func(ctx Context, _ string) error {
					return tc.set(ctx, []string{fmt.Sprintf("rows@%d", ctx.Width())})
				})
			})
			s.command()
			if got := s.frame(tc.method); got.Width != 80 || !slices.Equal(got.Lines, []string{"rows@80"}) {
				t.Fatalf("first push = %+v, want rows@80 tagged width 80", got)
			}
			s.width(60)
			s.command()
			if got := s.frame(tc.method); got.Width != 60 || !slices.Equal(got.Lines, []string{"rows@60"}) {
				t.Fatalf("push after resize = %+v, want rows@60 tagged width 60", got)
			}
		})
	}
}

func TestFooterAndHeaderRenderersRenderAtHostWidthAndFollowResize(t *testing.T) {
	for _, tc := range []struct {
		name, method string
		set          func(ctx Context, render func(width int) []string) error
	}{
		{"footer", "ui.setFooter", func(ctx Context, render func(int) []string) error { return ctx.SetFooterRenderer(render) }},
		{"header", "ui.setHeader", func(ctx Context, render func(int) []string) error { return ctx.SetHeaderRenderer(render) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := startWidthSurfaceHost(t, func(ext *Extension) {
				ext.Command("go", "install renderer", func(ctx Context, _ string) error {
					return tc.set(ctx, func(width int) []string { return []string{fmt.Sprintf("row@%d", width)} })
				})
				ext.Command("clear", "clear renderer", func(ctx Context, _ string) error {
					return tc.set(ctx, nil)
				})
			})
			s.command()
			if got := s.frame(tc.method); got.Width != 80 || !slices.Equal(got.Lines, []string{"row@80"}) {
				t.Fatalf("install = %+v, want row@80 tagged width 80", got)
			}
			// No extension action: the SDK re-renders at the host's new width.
			s.width(60)
			if got := s.frame(tc.method); got.Width != 60 || !slices.Equal(got.Lines, []string{"row@60"}) {
				t.Fatalf("after width_change = %+v, want row@60 tagged width 60", got)
			}
			s.width(114)
			if got := s.frame(tc.method); got.Width != 114 || !slices.Equal(got.Lines, []string{"row@114"}) {
				t.Fatalf("after width_change = %+v, want row@114 tagged width 114", got)
			}
			s.mustSend(envelope{Type: msgRequest, ID: "req-clear", Request: &requestMsg{Method: "command", Tool: "clear"}})
			if got := s.frame(tc.method); !got.Clear {
				t.Fatalf("clearing the renderer sent %+v, want clear", got)
			}
			s.width(90)
			s.quiet(tc.method)
		})
	}
}

// A static SetFooter replaces an installed renderer, so the renderer stops
// overwriting it after a resize.
func TestStaticFooterReplacesRenderer(t *testing.T) {
	s := startWidthSurfaceHost(t, func(ext *Extension) {
		ext.Command("go", "install renderer", func(ctx Context, _ string) error {
			return ctx.SetFooterRenderer(func(width int) []string { return []string{fmt.Sprintf("dyn@%d", width)} })
		})
		ext.Command("static", "static rows", func(ctx Context, _ string) error {
			return ctx.SetFooter([]string{"static"})
		})
	})
	s.command()
	_ = s.frame("ui.setFooter")
	s.mustSend(envelope{Type: msgRequest, ID: "req-static", Request: &requestMsg{Method: "command", Tool: "static"}})
	if got := s.frame("ui.setFooter"); !slices.Equal(got.Lines, []string{"static"}) || got.Width != 80 {
		t.Fatalf("static push = %+v", got)
	}
	s.width(60)
	s.quiet("ui.setFooter")
}

// A renderer that panics is reported through ui.notify and the previous rows
// stay, as the Node runtime reports "footer render failed" (runtime.mjs
// renderSpecialSurface) instead of ending the extension.
func TestFooterRendererPanicIsReportedNotFatal(t *testing.T) {
	s := startWidthSurfaceHost(t, func(ext *Extension) {
		ext.Command("go", "install renderer", func(ctx Context, _ string) error {
			return ctx.SetFooterRenderer(func(width int) []string {
				if width != 80 {
					panic("boom")
				}
				return []string{"ok"}
			})
		})
	})
	s.command()
	_ = s.frame("ui.setFooter")
	s.width(60)
	var notice struct {
		Message string `json:"message"`
		Level   string `json:"level"`
	}
	if err := json.Unmarshal(s.next("ui.notify"), &notice); err != nil {
		t.Fatal(err)
	}
	if notice.Level != "error" || notice.Message != "footer render failed: boom" {
		t.Fatalf("notice = %+v", notice)
	}
}

// Pi 0.87.1 interactive-mode.ts:2321-2336: a string[] widget is content the
// host lays out with Text(line, 1, 0) at the current width. The SDK therefore
// sends the list with no width; the host treats a width-less widget_push as
// that content, not as a pre-rendered frame.
func TestSetWidgetStringListIsSentAsContentWithoutWidth(t *testing.T) {
	s := startWidthSurfaceHost(t, func(ext *Extension) {
		ext.Command("go", "set widget", func(ctx Context, _ string) error {
			return ctx.SetWidget("status", []string{"● 3 agents running"})
		})
	})
	s.command()
	var args struct {
		Key   string   `json:"key"`
		Lines []string `json:"lines"`
		Width *int     `json:"width"`
	}
	if err := json.Unmarshal(s.next(msgWidgetPush), &args); err != nil {
		t.Fatal(err)
	}
	if args.Key != "status" || !slices.Equal(args.Lines, []string{"● 3 agents running"}) || args.Width != nil {
		t.Fatalf("widget_push = %+v", args)
	}
}

// Pi's ctx.ui.setWidget returns void without a host round trip
// (interactive-mode.ts:2300-2340). OnWidthChange handlers run on the message
// loop, so a string list SetWidget there must not wait for a reply: the Go SDK
// then matches the Rust and Python SDKs, whose handlers run on the loop that
// reads host replies and would stop reading the socket.
func TestSetWidgetStringListFromAWidthHandlerIsNotAHostCall(t *testing.T) {
	s := startWidthSurfaceHost(t, func(ext *Extension) {
		ext.Command("go", "subscribe", func(ctx Context, _ string) error {
			if _, err := ctx.OnWidthChange(func(c Context, width int) {
				_ = c.SetWidget("fit", []string{fmt.Sprintf("w@%d", width)})
			}); err != nil {
				return err
			}
			return ctx.SetWidget("fit", []string{fmt.Sprintf("w@%d", ctx.Width())})
		})
	})
	s.command()
	for _, width := range []int{80, 100, 90} {
		if width != 80 {
			s.width(width)
		}
		var args struct {
			Lines []string `json:"lines"`
		}
		if err := json.Unmarshal(s.next(msgWidgetPush), &args); err != nil {
			t.Fatal(err)
		}
		if want := []string{fmt.Sprintf("w@%d", width)}; !slices.Equal(args.Lines, want) {
			t.Fatalf("widget lines = %q, want %q", args.Lines, want)
		}
	}
}

// Pi's RPC mode writes one request per setWidget call, in call order and before the handler's response (rpc-mode.ts), so in RPC mode a string
// list is a ui.setWidget host call like every other shape: an unanswered widget_push frame has no place in that order.
func TestSetWidgetStringListInRPCModeIsAHostCall(t *testing.T) {
	s := startWidthSurfaceHostIn(t, "rpc", true, func(ext *Extension) {
		ext.Command("go", "set widget", func(ctx Context, _ string) error {
			return ctx.SetWidget("status", []string{"w-one"})
		})
	})
	s.command()
	var args struct {
		Key     string   `json:"key"`
		Content []string `json:"content"`
	}
	if err := json.Unmarshal(s.next("ui.setWidget"), &args); err != nil {
		t.Fatal(err)
	}
	if args.Key != "status" || !slices.Equal(args.Content, []string{"w-one"}) {
		t.Fatalf("ui.setWidget = %+v", args)
	}
}

// A retired renderer never renders or pushes again, so a refresh queued before
// SetFooter or SetFooterRenderer replaced it cannot overwrite the rows that
// replaced it.
func TestRetiredSurfaceRendererNeverPushes(t *testing.T) {
	s := &surfaceRenderer{method: footerMethod, render: func(int) []string {
		t.Fatal("a retired renderer rendered")
		return nil
	}}
	s.stop()
	if err := s.push(Context{ext: &Extension{}}); err != nil {
		t.Fatalf("push after stop = %v, want nil", err)
	}
}

// Pi calls a footer or header factory only in the TUI: rpc-mode.ts setFooter/setHeader and runner.ts noOpUIContext ignore it. The SDK must not call
// the renderer, send a frame for it, or re-render it after a resize, in RPC mode or without a UI.
func TestFooterAndHeaderRenderersAreNotRunOutsideTheTUI(t *testing.T) {
	for _, mode := range []struct {
		name  string
		mode  string
		hasUI bool
	}{{"rpc", "rpc", true}, {"print", "print", false}, {"json", "json", false}} {
		for _, tc := range []struct {
			name, method string
			set          func(ctx Context, render func(width int) []string) error
		}{
			{"footer", "ui.setFooter", func(ctx Context, render func(int) []string) error { return ctx.SetFooterRenderer(render) }},
			{"header", "ui.setHeader", func(ctx Context, render func(int) []string) error { return ctx.SetHeaderRenderer(render) }},
		} {
			t.Run(mode.name+"/"+tc.name, func(t *testing.T) {
				var mu sync.Mutex
				var rendered []int
				s := startWidthSurfaceHostIn(t, mode.mode, mode.hasUI, func(ext *Extension) {
					ext.Command("go", "install renderer", func(ctx Context, _ string) error {
						return tc.set(ctx, func(width int) []string {
							mu.Lock()
							rendered = append(rendered, width)
							mu.Unlock()
							return []string{"must-not-render"}
						})
					})
				})
				s.command()
				s.width(60)
				s.quiet(tc.method)
				s.command()
				s.quiet(tc.method)
				mu.Lock()
				defer mu.Unlock()
				if len(rendered) != 0 {
					t.Fatalf("the renderer ran at widths %v", rendered)
				}
			})
		}
	}
}

// rpc-mode.ts custom() resolves undefined without running the component; the host call keeps the dialog scope, as the Node runtime's does.
func TestCustomResolvesUndefinedInRPCModeWithoutRunningTheComponent(t *testing.T) {
	var mu sync.Mutex
	var ran []string
	record := func(what string) {
		mu.Lock()
		ran = append(ran, what)
		mu.Unlock()
	}
	results := make(chan string, 4)
	s := startWidthSurfaceHostIn(t, "rpc", true, func(ext *Extension) {
		ext.Command("go", "custom", func(ctx Context, _ string) error {
			value, err := ctx.Custom(&rpcProbeComponent{record: record}, nil)
			results <- fmt.Sprintf("component:%v:%v", value, err)
			value, err = ctx.Custom(struct{}{}, nil)
			results <- fmt.Sprintf("opaque:%v:%v", value, err)
			return nil
		})
	})
	s.command()
	for _, want := range []string{"component:<nil>:<nil>", "opaque:<nil>:<nil>"} {
		select {
		case got := <-results:
			if got != want {
				t.Fatalf("Custom = %s, want %s", got, want)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("no Custom result, want %s", want)
		}
	}
	for range 2 {
		var args map[string]string
		if err := json.Unmarshal(s.next("ui.custom"), &args); err != nil || len(args) != 1 || args["key"] == "" {
			t.Fatalf("ui.custom args = %v (%v), want only a key", args, err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(ran) != 0 {
		t.Fatalf("component callbacks ran in RPC mode: %v", ran)
	}
}

type rpcProbeComponent struct{ record func(string) }

func (c *rpcProbeComponent) Render(int) []string {
	c.record("render")
	return nil
}

func (c *rpcProbeComponent) HandleInput(string) (RemoteComponentResult, error) {
	c.record("input")
	return RemoteComponentResult{}, nil
}
