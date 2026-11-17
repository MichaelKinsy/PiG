package subprocess

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// renderersServe is the process half of a fused extension whose registration is regOf(generation). It answers the host's tool renderer resolution
// with renderers of its own whose shell is the generation ("gen 1", "gen 2", ...) and the host's Markdown transform with the text and the generation.
// crash ends its connection as a process crash does.
func renderersServe(name string, regOf func(generation int) *RegisterPayload) (serve func(net.Conn) error, crash func()) {
	var starts atomic.Int32
	var current atomic.Pointer[net.Conn]
	serve = func(conn net.Conn) error {
		generation := int(starts.Add(1))
		label := fmt.Sprintf("gen %d", generation)
		current.Store(&conn)
		reg := regOf(generation)
		reg.Name = name
		reg.Commands = []CommandDecl{{Name: "probe", Description: label}}
		if err := writeEnvelopeTo(conn, &Envelope{Type: MsgRegister, Register: reg}); err != nil {
			return err
		}
		for {
			env, err := readEnvelopeFrom(conn)
			if err != nil {
				return nil
			}
			switch env.Type {
			case MsgPing:
				if env.Ping != nil {
					_ = writeEnvelopeTo(conn, &Envelope{Type: MsgPong, Pong: &PongPayload{Nonce: env.Ping.Nonce}})
				}
			case MsgRequest:
				response := &ResponsePayload{}
				switch env.Request.Method {
				case RequestResolveToolRenderers:
					response.Result, _ = json.Marshal(ResolvedToolRenderers{Use: "own", RenderShell: label, RendersCall: true, Renderers: label})
				case RequestMarkdownTransform:
					var payload MarkdownTransformPayload
					_ = json.Unmarshal(env.Request.Args, &payload)
					response.Result, _ = json.Marshal(payload.Markdown + ":" + label)
				}
				_ = writeEnvelopeTo(conn, &Envelope{Type: MsgResponse, ID: env.ID, Response: response})
			}
		}
	}
	crash = func() {
		if conn := current.Load(); conn != nil {
			_ = (*conn).Close()
		}
	}
	return serve, crash
}

// loadRenderersRig loads a fused extension that registers regOf(generation) and returns it with the channel that reports each tool renderer
// resolution the process answered.
func loadRenderersRig(t *testing.T, regOf func(generation int) *RegisterPayload) (*restartRig, chan string) {
	t.Helper()
	h := NewHostWithConfigRoot(t.TempDir(), t.TempDir())
	h.SetUIBridge(newTestBridge(&mockUIContext{}))
	t.Cleanup(func() { h.Shutdown("test done") })
	recordCrashNotices(t, h)
	resolved := make(chan string, 16)
	h.SetToolRenderersResolvedFunc(func(tool string) {
		select {
		case resolved <- tool:
		default:
		}
	})
	serve, crash := renderersServe("renderers", regOf)
	if _, err := h.LoadInProcess(t.Context(), ExtConfig{Name: "renderers", Enabled: true, SupervisorConfig: restartSupervisor}, serve); err != nil {
		t.Fatal(err)
	}
	return newRestartRig(t, h, "renderers", func(*extension.Extension) func(context.Context) { return func(context.Context) { crash() } }), resolved
}

// resolvedShell draws the tool as the runner does: the extension's resolver is asked, the first resolution returns next() while the process answers,
// and the next one returns the answer. It returns the shell of the renderers the process answered.
func resolvedShell(t *testing.T, held *extension.Extension, resolved chan string) string {
	t.Helper()
	if len(held.ToolRenderers) == 0 {
		t.Fatal("the extension holds no tool renderer resolver")
	}
	resolve := func() *extension.ToolRenderers {
		return held.ToolRenderers[0]("greet", func() *extension.ToolRenderers { return nil })
	}
	if renderers := resolve(); renderers != nil {
		t.Fatalf("the first resolution returned %+v before the process answered", renderers)
	}
	select {
	case <-resolved:
	case <-time.After(testbudget.Wait(t)):
		t.Fatal("the process never answered the tool renderer resolution")
	}
	renderers := resolve()
	if renderers == nil || renderers.RenderCall == nil {
		t.Fatalf("resolved renderers = %+v, want the process's own with a call renderer", renderers)
	}
	return string(renderers.RenderShell)
}

// drawn is what the held extension's transformer, as the runner hands it out, makes of "x" on a surface; false when it holds none.
func drawn(held *extension.Extension, messageType extension.MarkdownMessageType) (string, bool) {
	registration := held.MarkdownTransformerRegistration()
	if registration == nil {
		return "", false
	}
	return registration.Scoped()("x", extension.MarkdownTransformContext{Context: context.Background(), MessageType: messageType, AvailableWidth: 80}), true
}

// A restarted extension draws its tool cards with the renderers its new process registered: the resolution asks the process of the generation that
// is running, not the dead one, and a card never keeps the old generation's answer.
func TestRestartedExtensionResolvesToolRenderersFromItsNewProcess(t *testing.T) {
	rig, resolved := loadRenderersRig(t, func(int) *RegisterPayload { return &RegisterPayload{ToolRenderers: 1} })
	if got := resolvedShell(t, rig.held, resolved); got != "gen 1" {
		t.Fatalf("before the crash the tool is drawn by %q, want gen 1", got)
	}
	rig.crash(t.Context())
	waitDeclarationGeneration(t, rig.h, "renderers", rig.held, "gen 2")
	if got := resolvedShell(t, rig.held, resolved); got != "gen 2" {
		t.Fatalf("after the restart the tool is drawn by %q, want gen 2", got)
	}
}

// The Markdown transformer of a restarted extension is the one its new process registered, in place of the old one: a transformer the new process
// registers transforms, one it no longer registers is gone, and the surfaces it lists are the new ones. The old closure is not kept.
func TestRestartedExtensionTakesItsNewMarkdownTransformer(t *testing.T) {
	user := []string{"user"}
	for _, tc := range []struct {
		name                      string
		first, second             *RegisterPayload
		firstAssistant            string // what generation 1 draws an assistant message as; "" when it registers no transformer
		afterNone                 bool   // generation 2 registers no transformer
		afterAssistant, afterUser string
	}{
		{name: "registered in both", first: &RegisterPayload{MarkdownTransformer: true}, second: &RegisterPayload{MarkdownTransformer: true}, firstAssistant: "x:gen 1", afterAssistant: "x:gen 2", afterUser: "x:gen 2"},
		{name: "registered only by the new process", first: &RegisterPayload{}, second: &RegisterPayload{MarkdownTransformer: true}, afterAssistant: "x:gen 2", afterUser: "x:gen 2"},
		{name: "no longer registered", first: &RegisterPayload{MarkdownTransformer: true}, second: &RegisterPayload{}, firstAssistant: "x:gen 1", afterNone: true},
		{name: "other surfaces", first: &RegisterPayload{MarkdownTransformer: true}, second: &RegisterPayload{MarkdownTransformer: true, MarkdownMessageTypes: &user}, firstAssistant: "x:gen 1", afterAssistant: "x", afterUser: "x:gen 2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rig, _ := loadRenderersRig(t, func(generation int) *RegisterPayload {
				reg := *tc.first
				if generation > 1 {
					reg = *tc.second
				}
				return &reg
			})
			got, held := drawn(rig.held, extension.MarkdownMessageAssistant)
			if tc.firstAssistant == "" && held {
				t.Fatal("generation 1 holds a transformer it did not register")
			}
			if tc.firstAssistant != "" && (!held || got != tc.firstAssistant) {
				t.Fatalf("generation 1 draws an assistant message as %q (transformer held: %t), want %q", got, held, tc.firstAssistant)
			}
			rig.crash(t.Context())
			waitDeclarationGeneration(t, rig.h, "renderers", rig.held, "gen 2")
			assistant, held := drawn(rig.held, extension.MarkdownMessageAssistant)
			if tc.afterNone {
				if held {
					t.Fatalf("the restarted extension still holds the transformer generation 1 registered (assistant message drawn as %q)", assistant)
				}
				return
			}
			if !held || assistant != tc.afterAssistant {
				t.Fatalf("the restarted extension draws an assistant message as %q (transformer held: %t), want %q", assistant, held, tc.afterAssistant)
			}
			if user, _ := drawn(rig.held, extension.MarkdownMessageUser); user != tc.afterUser {
				t.Fatalf("the restarted extension draws a user message as %q, want %q", user, tc.afterUser)
			}
		})
	}
}
