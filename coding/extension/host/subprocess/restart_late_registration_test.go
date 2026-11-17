package subprocess

import (
	"encoding/json"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

var lateRegistrationSupervisor = SupervisorConfig{MaxCrashes: 100, CrashWindow: time.Minute, InitialDelay: time.Millisecond, MaxDelay: 10 * time.Millisecond, BackoffFactor: 2}

// lateRegistration is a call a restarted extension makes right after its ready frame: it registers one more thing the way an SDK does from a
// timer or from work its factory started, and landed reports whether the held Extension has it.
type lateRegistration struct {
	name   string
	method string
	args   any
	landed func(*extension.Extension) bool
}

func lateRegistrations() []lateRegistration {
	enabled := true
	return []lateRegistration{
		{"command", CallRegisterDeclaration, &RegisterPayload{Commands: []CommandDecl{{Name: "late", Description: "late"}}}, func(e *extension.Extension) bool {
			_, ok := e.Command("late")
			return ok
		}},
		{"tool", CallRegisterTool, &ToolDecl{Name: "late", Description: "late", Parameters: []byte(`{"type":"object"}`)}, func(e *extension.Extension) bool {
			_, ok := e.RegisteredTool("late")
			return ok
		}},
		{"handler", "event.subscribe", map[string]any{"event": "turn_start", "handlerId": 2}, func(e *extension.Extension) bool {
			return e.HasEventHandlers("turn_start")
		}},
		{"thinking runs", CallOwnThinkingRuns, &OwnThinkingRunsPayload{Enabled: &enabled}, func(e *extension.Extension) bool {
			return e.OwnsThinkingRuns()
		}},
	}
}

// lateRegistrationServe is the process half of a fused extension. Its first connection registers "gen 1" and ends when crash is closed. Its second
// registers "gen 2" once held is closed, makes the late call as soon as it reads the ready frame and reports the host's answer on answered.
func lateRegistrationServe(late lateRegistration, crash, held <-chan struct{}, restarted chan<- struct{}, answered chan<- *ErrorInfo) func(net.Conn) error {
	var starts atomic.Int32
	return func(conn net.Conn) error {
		n := starts.Add(1)
		label := "gen 1"
		if n > 1 {
			label = "gen 2"
			if n == 2 {
				restarted <- struct{}{}
				<-held
			}
		}
		reg := &RegisterPayload{Name: "late", Commands: []CommandDecl{{Name: "probe", Description: label}}, Handlers: []HandlerDecl{{Event: "session_start", HandlerID: 1}}}
		if err := writeEnvelopeTo(conn, &Envelope{Type: MsgRegister, Register: reg}); err != nil {
			return err
		}
		if n == 1 {
			done := make(chan struct{})
			defer close(done)
			go func() {
				select {
				case <-crash:
					_ = conn.Close()
				case <-done:
				}
			}()
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
				_ = writeEnvelopeTo(conn, &Envelope{Type: MsgResponse, ID: env.ID, Response: &ResponsePayload{}})
			case MsgReady:
				if n != 2 {
					continue
				}
				args, err := json.Marshal(late.args)
				if err != nil {
					return err
				}
				if err := writeEnvelopeTo(conn, &Envelope{Type: MsgCall, ID: "late", Call: &CallPayload{Method: late.method, Args: args}}); err != nil {
					return err
				}
			case MsgCallResult:
				if env.ID == "late" {
					answered <- env.CallResult.Error
				}
			}
		}
	}
}

// What a restarted extension registers while its adoption is still pending (after its ready frame, before the host rewrites the Extension its runner holds)
// is part of the registration the host serves once the restart settles: the adoption replaces what the old process registered, not what the new one
// registered after its ready. The host's adoption is held at the reset of the tool renderer state, which comes after the host's loop for the
// connection's calls starts and before the old order's rewrite of the Extension, so the call is answered while the adoption has not run.
func TestRestartKeepsWhatTheNewProcessRegistersAfterItsReady(t *testing.T) {
	for _, late := range lateRegistrations() {
		t.Run(late.name, func(t *testing.T) {
			h := NewHostWithConfigRoot(t.TempDir(), t.TempDir())
			t.Cleanup(func() { h.Shutdown("test done") })
			crash, held := make(chan struct{}), make(chan struct{})
			restarted, answered := make(chan struct{}, 1), make(chan *ErrorInfo, 1)
			serve := lateRegistrationServe(late, crash, held, restarted, answered)
			if _, err := h.LoadInProcess(t.Context(), ExtConfig{Name: "late", Enabled: true, SupervisorConfig: lateRegistrationSupervisor}, serve); err != nil {
				t.Fatal(err)
			}
			h.mu.Lock()
			me := h.exts["late"]
			h.mu.Unlock()
			if me == nil || me.ext == nil {
				t.Fatal("the extension is not published")
			}
			ext := me.ext
			if late.landed(ext) {
				t.Fatalf("the first generation already has the late %s", late.name)
			}
			close(crash)
			wait := testbudget.Wait(t)
			select {
			case <-restarted:
			case <-time.After(wait):
				t.Fatal("the extension was not restarted")
			}
			func() {
				state := &me.toolRendererResolvers
				state.mu.Lock()
				defer state.mu.Unlock()
				close(held)
				select {
				case failure := <-answered:
					if failure != nil {
						t.Fatalf("the late %s was refused: %+v", late.name, failure)
					}
				case <-time.After(wait):
					t.Fatalf("the late %s was not answered", late.name)
				}
			}()
			deadline := time.Now().Add(wait)
			for {
				if command, ok := ext.Command("probe"); ok && command.Description == "gen 2" {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("the restart did not adopt the second generation")
				}
				time.Sleep(time.Millisecond)
			}
			// The adoption holds Host.mu from its first rewrite to its last: read under it.
			h.mu.Lock()
			landed := late.landed(ext)
			h.mu.Unlock()
			if !landed {
				t.Fatalf("the late %s the restarted extension registered after its ready is lost: the adoption replaced it", late.name)
			}
			if command, ok := ext.Command("probe"); !ok || command.Description != "gen 2" {
				t.Fatalf("the held Extension serves %+v, want the second generation's probe command", command)
			}
		})
	}
}
