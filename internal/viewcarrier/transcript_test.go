package viewcarrier

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/uiview"
)

var updateTranscripts = flag.Bool("update-transcripts", false, "rewrite testdata/transcripts from what the carrier writes")

// script is one conversation with the carrier, written down as a client and a relay read it: `C: ` is a line the client
// sent, `H: ` a line the host wrote, in order, and `# ` says what happened in the host in between.
type script struct {
	f   *fixture
	out []string
	// read is how many of the host's lines are written down.
	read int
}

func (s *script) note(text string) { s.out = append(s.out, "# "+text) }

// host writes down the lines the host wrote since the last call.
func (s *script) host() {
	s.f.t.Helper()
	s.f.settled()
	lines := s.f.w.lines()
	for _, line := range lines[s.read:] {
		s.out = append(s.out, "H: "+strings.TrimSuffix(line, "\n"))
	}
	s.read = len(lines)
}

// client sends a command line and writes down what the host wrote for it.
func (s *script) client(line string) {
	s.f.t.Helper()
	s.out = append(s.out, "C: "+line)
	s.f.do(line)
	s.host()
}

// settle waits for the reply of an intent in flight.
func (s *script) settle(id string) {
	s.f.t.Helper()
	eventually(s.f.t, "intent "+id+" never settled", func() bool {
		r := s.f.w.response(id, "ui_view_intent")
		return r != nil && r["data"] != nil
	})
	s.host()
}

const transcriptAbilities = `{"slots":["panel","tool-card"],"nodes":["text","box"],"markdown":true,"imageFormats":["png"],` +
	`"limits":{"nodes":64,"depth":8,"snapshotBytes":4096,"textBytes":1024,"mounts":8,"intentArgBytes":64},"input":{"keyboard":true,"pointer":true,"text":false},` +
	`"claims":[{"name":"display.colors","value":256}],"slotClaims":[{"slot":"panel","claims":[{"name":"display.colors","value":16}]}],` +
	`"drawing":{"profiles":["scene"],"shapes":8,"pixels":0}}`

// transcripts is one conversation per message of the carrier. Each shows the message with every member it has, its
// reply, and each error it answers.
var transcripts = map[string]func(s *script){
	"ui_view_attach": func(s *script) {
		s.note("ui_view_attach {abilities} is Registry.Attach. The reply is the attachment's number, the number of the last delivery made and every current mount resolved for the client.")
		s.client(`{"id":"1","type":"ui_view_attach","abilities":` + transcriptAbilities + `}`)
		s.note("One attachment per connection at a time.")
		s.client(`{"id":"2","type":"ui_view_attach","abilities":` + transcriptAbilities + `}`)
		s.note("An unknown member, here or inside a record, is invalid; so is a missing one.")
		s.client(`{"id":"3","type":"ui_view_attach","abilities":` + transcriptAbilities + `,"version":1}`)
		s.client(`{"id":"4","type":"ui_view_attach","abilities":` + strings.Replace(transcriptAbilities, `"markdown":true`, `"markdown":true,"html":true`, 1) + `}`)
		s.client(`{"id":"5","type":"ui_view_attach"}`)
		s.note("After a detach: abilities outside the record are the registry's invalid.")
		s.client(`{"id":"6","type":"ui_view_detach","attachment":1}`)
		s.client(`{"id":"7","type":"ui_view_attach","abilities":` + strings.Replace(transcriptAbilities, `"profiles":["scene"]`, `"profiles":["hologram"]`, 1) + `}`)
		s.client(`{"id":"8","type":"ui_view_attach","abilities":` + transcriptAbilities + `}`)
		s.note("A plugin mounts a view; then another connection attaches and its reply carries the mount.")
		s.f.mount("k", "hello", "")
		s.host()
		second := &wire{t: s.f.t}
		second.wake = sync.NewCond(&second.mu)
		c2 := s.f.connect(second)
		line := `{"id":"1","type":"ui_view_attach","abilities":` + abilitiesJSON(4096) + `}`
		s.out = append(s.out, "C: "+line)
		s.f.doOn(c2, second, line)
		s.out = append(s.out, "H: "+strings.TrimSuffix(second.lines()[0], "\n"))
		s.note("A session without an extension host has no registry.")
		none := &wire{t: s.f.t}
		none.wake = sync.NewCond(&none.mu)
		c3 := New(Transport{Host: func() Host { return nil }, Write: none.write, Flushed: none.flushed})
		s.out = append(s.out, "C: "+line)
		s.f.doOn(c3, none, line)
		s.out = append(s.out, "H: "+strings.TrimSuffix(none.lines()[0], "\n"))
	},
	"ui_view_abilities": func(s *script) {
		s.note("ui_view_abilities {attachment, abilities} is Attachment.UpdateAbilities. The reply is {}; the mounts that now resolve differently follow as ui_view events.")
		s.client(`{"id":"1","type":"ui_view_attach","abilities":` + transcriptAbilities + `}`)
		s.f.mount("k", "hello", `,"alternatives":[{"when":{"any":[{"all":[{"claim":"display.colors","op":"atLeast","value":200}]}]},"view":{"root":{"kind":"text","text":"hello in color"}}}]`)
		s.host()
		s.note("The panel now claims 256 colors, so the alternative is what this client draws.")
		s.client(`{"id":"2","type":"ui_view_abilities","attachment":1,"abilities":` + strings.Replace(transcriptAbilities, `"value":16`, `"value":256`, 1) + `}`)
		s.note("The same abilities again change nothing.")
		s.client(`{"id":"3","type":"ui_view_abilities","attachment":1,"abilities":` + strings.Replace(transcriptAbilities, `"value":16`, `"value":256`, 1) + `}`)
		s.client(`{"id":"4","type":"ui_view_abilities","attachment":7,"abilities":` + transcriptAbilities + `}`)
		s.client(`{"id":"5","type":"ui_view_abilities","attachment":1}`)
	},
	"ui_view_resync": func(s *script) {
		s.note("ui_view_resync {attachment} is Attachment.Resync. The client drops every event numbered at or below the reply's seq and redraws only what has another digest.")
		s.client(`{"id":"1","type":"ui_view_attach","abilities":` + transcriptAbilities + `}`)
		ref := s.f.mount("k", "one", "")
		s.f.update(ref, "two")
		s.f.mount("j", "other", "")
		s.host()
		s.client(`{"id":"2","type":"ui_view_resync","attachment":1}`)
		s.client(`{"id":"3","type":"ui_view_resync","attachment":2}`)
		s.client(`{"id":"4","type":"ui_view_resync","attachment":1,"since":2}`)
	},
	"ui_view_detach": func(s *script) {
		s.note("ui_view_detach {attachment} is Attachment.Detach. Closing the connection does the same.")
		s.client(`{"id":"1","type":"ui_view_attach","abilities":` + transcriptAbilities + `}`)
		s.client(`{"id":"2","type":"ui_view_detach","attachment":1}`)
		s.note("The attachment ended: its number is stale, and the connection may attach again.")
		s.client(`{"id":"3","type":"ui_view_detach","attachment":1}`)
		s.client(`{"id":"4","type":"ui_view_attach","abilities":` + transcriptAbilities + `}`)
	},
	"ui_view_intent": func(s *script) {
		s.note("ui_view_intent {attachment, plugin, mount {key, epoch, rev}, nodeKey?, intent, arg} is Attachment.Invoke. The reply comes once the intent settled and is its result {ticket, outcome, answer?, error?}.")
		s.client(`{"id":"1","type":"ui_view_attach","abilities":` + transcriptAbilities + `}`)
		s.f.mount("k", "one", `,"intents":[{"name":"add","nodeKey":"n","arg":{"type":"object","properties":{"n":{"type":"integer"}},"required":["n"]},"answer":{"type":"object","properties":{"sum":{"type":"integer"}},"required":["sum"]}}]`)
		s.host()
		intent := func(id, members string) string {
			if id != "" {
				id = `"id":"` + id + `",`
			}
			return `{` + id + `"type":"ui_view_intent","attachment":1,"plugin":"p",` + members + `}`
		}
		s.note("done: the plugin's answer.")
		s.client(intent("2", `"mount":{"key":"k","epoch":1,"rev":1},"nodeKey":"n","intent":"add","arg":{"n":1}`))
		s.settle("2")
		s.note("refused by the host: the argument is outside the declared shape, and the plugin is not reached.")
		s.client(intent("3", `"mount":{"key":"k","epoch":1,"rev":1},"nodeKey":"n","intent":"add","arg":{"n":"one"}`))
		s.settle("3")
		s.note("stale: the mount ref is not the current one.")
		s.client(intent("4", `"mount":{"key":"k","epoch":1,"rev":9},"nodeKey":"n","intent":"add","arg":{"n":1}`))
		s.settle("4")
		s.note("stale: the connection does not hold the attachment. The reply is still a result.")
		s.client(strings.Replace(intent("5", `"mount":{"key":"k","epoch":1,"rev":1},"nodeKey":"n","intent":"add","arg":{"n":1}`), `"attachment":1`, `"attachment":9`, 1))
		s.note("An intent without an id settles like any other; its reply has no id and it cannot be cancelled.")
		s.out = append(s.out, "C: "+intent("", `"mount":{"key":"k","epoch":1,"rev":1},"nodeKey":"n","intent":"add","arg":{"n":1}`))
		s.f.do(intent("", `"mount":{"key":"k","epoch":1,"rev":1},"nodeKey":"n","intent":"add","arg":{"n":1}`))
		eventually(s.f.t, "the intent without an id never settled", func() bool { return len(s.f.w.lines()) > s.read })
		s.host()
		s.note("A malformed command is an error response with code invalid.")
		s.client(intent("6", `"mount":{"key":"k","epoch":1,"rev":1},"intent":"add"`))
		s.client(intent("7", `"mount":{"key":"k","epoch":1,"rev":1},"intent":"add","arg":{},"ticket":"t9"`))
	},
	"ui_view_intent_cancel": func(s *script) {
		s.note("ui_view_intent_cancel is the transport's request cancel: its id is the id of the intent request. Its own reply comes first, then the intent's result, cancelled.")
		s.client(`{"id":"1","type":"ui_view_attach","abilities":` + transcriptAbilities + `}`)
		s.f.mount("k", "one", `,"intents":[{"name":"wait","arg":{"type":"object"},"cancellable":true,"timeoutMs":60000}]`)
		s.host()
		s.client(`{"id":"2","type":"ui_view_intent","attachment":1,"plugin":"p","mount":{"key":"k","epoch":1,"rev":1},"intent":"wait","arg":{}}`)
		<-s.f.p.started
		s.note("The plugin's handler is running.")
		s.out = append(s.out, `C: {"id":"2","type":"ui_view_intent_cancel"}`)
		s.f.do(`{"id":"2","type":"ui_view_intent_cancel"}`)
		s.settle("2")
		s.note("No intent in flight has the id any more.")
		s.client(`{"id":"2","type":"ui_view_intent_cancel"}`)
		s.client(`{"type":"ui_view_intent_cancel"}`)
		s.client(`{"id":"3","type":"ui_view_intent_cancel","attachment":1}`)
	},
	"ui_view_report": func(s *script) {
		s.note("ui_view_report {attachment, plugin, mount {key, epoch}, notice {code, severity, message?, path?, claim?, measured?, limit?}} is Attachment.Report: the client could not draw part of a mount, and the plugin reads the notice with ui.view.diagnostics.")
		s.client(`{"id":"1","type":"ui_view_attach","abilities":` + transcriptAbilities + `}`)
		s.f.mount("k", "one", "")
		s.host()
		s.client(`{"id":"2","type":"ui_view_report","attachment":1,"plugin":"p","mount":{"key":"k","epoch":1},"notice":{"code":"undrawn","severity":"warning","message":"no room for the box","path":"root/0"}}`)
		s.client(`{"id":"3","type":"ui_view_report","attachment":1,"plugin":"p","mount":{"key":"k","epoch":1},"notice":{"code":"limit","severity":"error","claim":"display.colors","measured":300,"limit":256}}`)
		s.client(`{"id":"4","type":"ui_view_report","attachment":1,"plugin":"p","mount":{"key":"k","epoch":1},"notice":{"code":"unsupported","severity":"info"}}`)
		s.note("A client reports undrawn, unsupported, limit or unfocused; the mount must be the current incarnation of its key.")
		s.client(`{"id":"5","type":"ui_view_report","attachment":1,"plugin":"p","mount":{"key":"k","epoch":1},"notice":{"code":"refused","severity":"info"}}`)
		s.client(`{"id":"6","type":"ui_view_report","attachment":1,"plugin":"p","mount":{"key":"k","epoch":2},"notice":{"code":"undrawn","severity":"info"}}`)
		s.client(`{"id":"7","type":"ui_view_report","attachment":1,"plugin":"p","mount":{"key":"gone","epoch":1},"notice":{"code":"undrawn","severity":"info"}}`)
		s.client(`{"id":"8","type":"ui_view_report","attachment":3,"plugin":"p","mount":{"key":"k","epoch":1},"notice":{"code":"undrawn","severity":"info"}}`)
		s.client(`{"id":"9","type":"ui_view_report","attachment":1,"plugin":"p","mount":{"key":"k","epoch":1,"rev":1},"notice":{"code":"undrawn","severity":"info"}}`)
	},
	"ui_view": func(s *script) {
		s.note("ui_view {attachment, seq, snapshot} is one Delivery: the whole of one mount as this client draws it. Events of an attachment are numbered 1, 2, 3 without a hole.")
		s.client(`{"id":"1","type":"ui_view_attach","abilities":` + transcriptAbilities + `}`)
		s.note("A mount with a target, intents, an inline asset, a drawing, a11y and focus records.")
		a, err := uiview.DecodeMount([]byte(`{"key":"card","slot":"tool-card","target":{"toolCallId":"call-1"},` +
			`"view":{"root":{"kind":"box","id":"b","children":[{"kind":"text","text":"result"}]}},` +
			`"intents":[{"name":"p.open","nodeKey":"b","arg":{"type":"object"},"cancellable":true,"timeoutMs":5000}],` +
			`"assets":[{"id":"img","mime":"image/png","size":4,"sha256":"` + strings.Repeat("ab", 32) + `","delivery":"inline"}],` +
			`"drawing":{"profile":"scene","shapes":[{"kind":"rect","x":0,"y":0,"width":4,"height":2,"fill":"#336699"}]},` +
			`"a11y":[{"node":"b","name":"Result","role":"button"}],"focus":[{"node":"b","request":"mount","restore":"previous"}]}`))
		if err != nil {
			s.f.t.Fatal(err)
		}
		card, err := s.f.h.reg.Mount("p", a)
		if err != nil {
			s.f.t.Fatal(err)
		}
		s.host()
		s.note("Hidden, shown again, then finished with a result: every change is a full snapshot.")
		for _, op := range []string{"hide", "show"} {
			if _, err := s.f.h.reg.Transition("p", op, uiview.RefArgs{Mount: card.Mount}); err != nil {
				s.f.t.Fatal(err)
			}
		}
		if _, err := s.f.h.reg.Finish("p", uiview.FinishArgs{Mount: card.Mount, Result: json.RawMessage(`{"opened":true}`)}); err != nil {
			s.f.t.Fatal(err)
		}
		s.host()
		s.note("A mount with an alternative for clients that claim at least 16 colors on the panel: this client is sent alternative 1.")
		ref := s.f.mount("k", "plain", `,"alternatives":[{"when":{"any":[{"all":[{"claim":"display.colors","op":"atLeast","value":16}]}]},"view":{"root":{"kind":"text","text":"colorful"}}}]`)
		s.host()
		s.note("Another client that takes more text attaches, so the host admits an update past this client's textBytes; this client is sent the fallback, degraded.")
		var wide uiview.ClientAbilities
		if err := json.Unmarshal([]byte(abilitiesJSON(4096)), &wide); err != nil {
			s.f.t.Fatal(err)
		}
		if _, _, err := s.f.h.reg.Attach(wide, nil); err != nil {
			s.f.t.Fatal(err)
		}
		big, err := uiview.DecodeUpdate(fmt.Appendf(nil, `{"mount":{"key":"k","epoch":%d,"rev":%d},"view":{"root":{"kind":"text","text":%q}},"fallback":{"kind":"text","text":"too long"}}`, ref.Epoch, ref.Rev, strings.Repeat("x", 2000)))
		if err != nil {
			s.f.t.Fatal(err)
		}
		if _, err := s.f.h.reg.Update("p", big); err != nil {
			s.f.t.Fatal(err)
		}
		s.host()
		s.note("The host ends every mount, and says why.")
		s.f.h.reg.Break("", uiview.ReasonShutdown)
		s.host()
	},
	"ui_view_gap": func(s *script) {
		s.note("ui_view_gap {attachment, seq}: the client stopped reading, the queue of 256 events filled, and the host refused delivery number seq. The client is written the queued events, then this line, then nothing more for the attachment until it resyncs.")
		s.client(`{"id":"1","type":"ui_view_attach","abilities":` + transcriptAbilities + `}`)
		s.f.w.stall()
		ref := s.f.mount("k", "v1", "")
		for i := 2; i <= maxQueueEvents+40; i++ {
			ref = s.f.update(ref, "v"+strconv.Itoa(i))
		}
		s.f.w.resume()
		s.f.settled()
		lines := s.f.w.lines()
		s.note("The ui_view events 1 to " + strconv.Itoa(len(lines)-s.read-2) + " come first, then:")
		s.read = len(lines) - 2
		s.host()
		s.note("The plugin updated the mount 40 more times; none of it was written. The resync's seq is the gap's.")
		s.client(`{"id":"2","type":"ui_view_resync","attachment":1}`)
		s.note("The next event has the next number.")
		s.f.update(ref, "after")
		s.host()
	},
}

// TestCarrierTranscripts pins the bytes of every message of the carrier: one transcript per message under
// testdata/transcripts, which a client or a relay on another protocol builds against. A change of a member's name, of
// the member order, of a code or of an error text changes a transcript.
func TestCarrierTranscripts(t *testing.T) {
	names := []string{"ui_view_attach", "ui_view_abilities", "ui_view_resync", "ui_view_detach", "ui_view_intent", "ui_view_intent_cancel", "ui_view_report", "ui_view", "ui_view_gap"}
	if len(names) != len(transcripts) {
		t.Fatalf("%d transcripts for %d messages", len(transcripts), len(names))
	}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			s := &script{f: newFixture(t)}
			transcripts[name](s)
			got := strings.Join(s.out, "\n") + "\n"
			path := filepath.Join("testdata", "transcripts", name+".txt")
			if *updateTranscripts {
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if got != string(want) {
				gl, wl := strings.Split(got, "\n"), strings.Split(string(want), "\n")
				for i := range gl {
					if i >= len(wl) || gl[i] != wl[i] {
						w := "(no line)"
						if i < len(wl) {
							w = wl[i]
						}
						t.Fatalf("%s line %d:\n  got  %.600s\n  want %.600s\n(-update-transcripts rewrites the file; a changed message shape needs approval)", path, i+1, gl[i], w)
					}
				}
				t.Fatalf("%s has %d lines, the carrier wrote %d", path, len(wl), len(gl))
			}
		})
	}
}
