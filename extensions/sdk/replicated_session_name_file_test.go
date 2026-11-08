package sdk

import (
	"encoding/json"
	"strings"
	"testing"
)

// The session name and file are replicated with the model and the id: the host pushes them where it pushes those (a name write publishes before its reply, /name and a replaced Session report through hostStateChanged), and each snapshot's Session replaces them, "" being none. The leaf is not: it moves with every committed entry, which no push announces, so it stays a host call.

type sessionNameFileReads struct {
	name, file, managerName, managerFile *string
	errs                                 []error
}

func readSessionNameFile(ext *Extension, edit func(*Context)) *sessionNameFileReads {
	reads := &sessionNameFileReads{}
	ext.Command("read", "read", func(ctx Context, _ string) error {
		if edit != nil {
			edit(&ctx)
		}
		var err error
		for _, read := range []func() error{
			func() (err error) { reads.name, err = ctx.GetSessionName(); return },
			func() (err error) { reads.file, err = ctx.GetSessionFile(); return },
			func() (err error) { reads.managerName, err = ctx.SessionManager().GetSessionName(); return },
			func() (err error) { reads.managerFile, err = ctx.SessionManager().GetSessionFile(); return },
		} {
			if err = read(); err != nil {
				reads.errs = append(reads.errs, err)
			}
		}
		return nil
	})
	return reads
}

func str(p *string) string {
	if p == nil {
		return "<nil>"
	}
	return *p
}

func TestSessionNameAndFileAreReadFromTheStateSnapshots(t *testing.T) {
	ready := &readyMsg{Cwd: "/tmp", Width: 80, State: json.RawMessage(`{"session":{"sessionId":"s1","sessionName":"first","sessionFile":"/s/one.jsonl"}}`)}
	ext := New("replica-name-file")
	reads := readSessionNameFile(ext, nil)
	host, _, done := surfaceHost(t, ext, ready)
	defer surfaceShutdown(t, host, done)
	check := func(step, name, file string) {
		t.Helper()
		*reads = sessionNameFileReads{}
		calls, resp := runSurfaceCommand(t, host, "read", func(*callMsg) *callResultMsg { return &callResultMsg{} })
		if resp.Error != nil || len(calls) != 0 || len(reads.errs) != 0 {
			t.Fatalf("%s: calls %v, errors %v, response %+v: the reads must send no host call", step, calls, reads.errs, resp.Error)
		}
		for what, got := range map[string]*string{"GetSessionName": reads.name, "manager GetSessionName": reads.managerName} {
			if str(got) != name {
				t.Errorf("%s: %s = %s, want %q", step, what, str(got), name)
			}
		}
		for what, got := range map[string]*string{"GetSessionFile": reads.file, "manager GetSessionFile": reads.managerFile} {
			if str(got) != file {
				t.Errorf("%s: %s = %s, want %q", step, what, str(got), file)
			}
		}
	}
	check("ready", "first", "/s/one.jsonl")
	sendState(t, host, `{"session":{"sessionId":"s2","sessionName":"second","sessionFile":"/s/two.jsonl"}}`)
	check("replaced Session", "second", "/s/two.jsonl")
	sendState(t, host, `{"isIdle":true}`)
	check("absent session keeps", "second", "/s/two.jsonl")
	// An in-memory Session with no name: "" is none, and it clears both, as the host's own empty reply decoded to nil.
	sendState(t, host, `{"session":{"sessionId":"s3","sessionName":"","sessionFile":""}}`)
	check("in-memory Session", "<nil>", "<nil>")
}

// The leaf is not replicated: a read asks the host even after a snapshot.
func TestSessionLeafIsStillAHostCall(t *testing.T) {
	ready := &readyMsg{Cwd: "/tmp", Width: 80, State: json.RawMessage(`{"session":{"sessionId":"s1","sessionName":"n","sessionFile":"/f"}}`)}
	ext := New("replica-leaf")
	var leaf *string
	ext.Command("read", "read", func(ctx Context, _ string) (err error) {
		leaf, err = ctx.GetLeafID()
		return err
	})
	host, _, done := surfaceHost(t, ext, ready)
	defer surfaceShutdown(t, host, done)
	calls, _ := runSurfaceCommand(t, host, "read", func(*callMsg) *callResultMsg {
		return &callResultMsg{Result: json.RawMessage(`{"leafId":"leaf-from-host"}`)}
	})
	if len(calls) != 1 || calls[0].Method != "getLeafID" || leaf == nil || *leaf != "leaf-from-host" {
		t.Fatalf("calls %v, leaf %v", calls, leaf)
	}
}

// Before the first snapshot, in a replacement context and in a stale one the reads behave as the model and id reads do.
func TestSessionNameAndFileFallbacks(t *testing.T) {
	for _, tc := range []struct {
		name      string
		ready     *readyMsg
		edit      func(*Extension, *Context)
		wantCalls string
		wantStale bool
	}{
		{name: "before the first snapshot", ready: nil, wantCalls: "getSessionName,getSessionFile,sessionRead,sessionRead"},
		{name: "replacement context", ready: &readyMsg{Cwd: "/tmp", Width: 80, State: json.RawMessage(`{"session":{"sessionId":"s","sessionName":"n","sessionFile":"/f"}}`)},
			edit: func(_ *Extension, c *Context) { c.replacement = &replacementState{} }, wantCalls: "getSessionName,getSessionFile,sessionRead,sessionRead"},
		{name: "stale context", ready: &readyMsg{Cwd: "/tmp", Width: 80, State: json.RawMessage(`{"session":{"sessionId":"s","sessionName":"n","sessionFile":"/f"}}`)},
			edit: func(e *Extension, _ *Context) { m := "stale after session replacement or reload"; e.stale.Store(&m) }, wantStale: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ext := New("replica-name-file-fallback")
			reads := readSessionNameFile(ext, func(c *Context) {
				if tc.edit != nil {
					tc.edit(ext, c)
				}
			})
			host, _, done := surfaceHost(t, ext, tc.ready)
			defer surfaceShutdown(t, host, done)
			calls, _ := runSurfaceCommand(t, host, "read", func(*callMsg) *callResultMsg {
				return &callResultMsg{Result: json.RawMessage(`{"name":"from-host","sessionFile":"/host","x":1}`)}
			})
			var methods []string
			for _, call := range calls {
				methods = append(methods, call.Method)
			}
			if got := strings.Join(methods, ","); got != tc.wantCalls {
				t.Fatalf("calls = %q, want %q", got, tc.wantCalls)
			}
			if tc.wantStale {
				if len(reads.errs) != 4 {
					t.Fatalf("errors = %v, want the stale message from all four reads", reads.errs)
				}
				for _, err := range reads.errs {
					if !strings.Contains(err.Error(), "stale after session replacement") {
						t.Errorf("error = %v", err)
					}
				}
			}
		})
	}
}
