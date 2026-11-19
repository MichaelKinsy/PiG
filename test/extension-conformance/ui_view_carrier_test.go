package extensionconformance

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/coding/uiview"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/viewcarrier"
)

// carrierTail is the connection's ordered output as the rpc loop has it: a write appends and returns, one goroutine
// writes to the connection, and flushed returns once it wrote everything.
type carrierTail struct {
	mu      sync.Mutex
	wake    *sync.Cond
	out     io.Writer
	pending [][]byte
	writing bool
	closed  bool
}

func newCarrierTail(out io.Writer) *carrierTail {
	q := &carrierTail{out: out}
	q.wake = sync.NewCond(&q.mu)
	go func() {
		q.mu.Lock()
		defer q.mu.Unlock()
		for {
			for len(q.pending) == 0 && !q.closed {
				q.wake.Wait()
			}
			if q.closed {
				return
			}
			batch := q.pending
			q.pending, q.writing = nil, true
			q.mu.Unlock()
			for _, line := range batch {
				if _, err := q.out.Write(line); err != nil {
					break
				}
			}
			q.mu.Lock()
			q.writing = false
			q.wake.Broadcast()
		}
	}()
	return q
}

func (q *carrierTail) write(line []byte) {
	q.mu.Lock()
	q.pending = append(q.pending, line)
	q.wake.Broadcast()
	q.mu.Unlock()
}

func (q *carrierTail) flushed(ctx context.Context) bool {
	defer context.AfterFunc(ctx, func() {
		q.mu.Lock()
		q.wake.Broadcast()
		q.mu.Unlock()
	})()
	q.mu.Lock()
	defer q.mu.Unlock()
	for (len(q.pending) > 0 || q.writing) && !q.closed && ctx.Err() == nil {
		q.wake.Wait()
	}
	return ctx.Err() == nil
}

func (q *carrierTail) close() {
	q.mu.Lock()
	q.closed = true
	q.wake.Broadcast()
	q.mu.Unlock()
}

// carrierClient is a client outside the host: it writes command lines and reads the lines it is written.
type carrierClient struct {
	t     *testing.T
	in    io.Writer
	lines chan string
	// gate is held while the client does not read.
	gate sync.Mutex
	seen []string
}

// serveCarrier serves the plugin UI carrier for bridge on a line stream of the named transport, the way the rpc loop
// does for its connection (one carrier, commands in arrival order, replies and events through one ordered output), and
// returns the client at the other end. "pipe" is a pair of OS pipes, as a process's stdin and stdout; "socket" is a
// connection accepted on a Unix socket, as `--listen` serves.
func serveCarrier(t *testing.T, bridge *subprocess.UIBridge, transport string) *carrierClient {
	t.Helper()
	var hostIn io.ReadCloser
	var hostOut io.WriteCloser
	var clientIn io.WriteCloser
	var clientOut io.ReadCloser
	switch transport {
	case "pipe":
		var err error
		if hostIn, clientIn, err = os.Pipe(); err != nil {
			t.Fatal(err)
		}
		if clientOut, hostOut, err = os.Pipe(); err != nil {
			t.Fatal(err)
		}
	case "socket":
		dir, err := os.MkdirTemp("", "pig-carrier")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.RemoveAll(dir) })
		listener, err := net.Listen("unix", filepath.Join(dir, "s"))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = listener.Close() }()
		dialed, err := net.Dial("unix", listener.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		accepted, err := listener.Accept()
		if err != nil {
			t.Fatal(err)
		}
		hostIn, hostOut, clientIn, clientOut = accepted, accepted, dialed, dialed
	default:
		t.Fatalf("unknown transport %s", transport)
	}
	tail := newCarrierTail(hostOut)
	carrier := viewcarrier.New(viewcarrier.Transport{
		Host:    func() viewcarrier.Host { return bridge },
		Write:   tail.write,
		Flushed: tail.flushed,
	})
	served := make(chan struct{})
	go func() {
		defer close(served)
		reader := bufio.NewReaderSize(hostIn, 1<<16)
		for {
			line, err := reader.ReadBytes('\n')
			if len(line) > 0 {
				var head struct {
					ID   json.RawMessage `json:"id"`
					Type string          `json:"type"`
				}
				if json.Unmarshal(line, &head) == nil && viewcarrier.Handles(head.Type) {
					carrier.Handle(head.ID, head.Type, line, func(response any, written func()) {
						if raw, err := jsstring.MarshalJSON(response); err == nil {
							tail.write(append(raw, '\n'))
						}
						if written != nil {
							written()
						}
					})
				}
			}
			if err != nil {
				return
			}
		}
	}()
	c := &carrierClient{t: t, in: clientIn, lines: make(chan string, 1<<16)}
	go func() {
		defer close(c.lines)
		reader := bufio.NewReaderSize(clientOut, 1<<12)
		for {
			c.passGate()
			line, err := reader.ReadString('\n')
			if line != "" {
				c.lines <- strings.TrimSuffix(line, "\n")
			}
			if err != nil {
				return
			}
		}
	}()
	t.Cleanup(func() {
		_ = clientIn.Close()
		_ = hostIn.Close()
		<-served
		carrier.Close()
		tail.close()
		_ = hostOut.Close()
		_ = clientOut.Close()
	})
	return c
}

func (c *carrierClient) send(line string) {
	c.t.Helper()
	if _, err := io.WriteString(c.in, line+"\n"); err != nil {
		c.t.Fatal(err)
	}
}

// until reads lines until done reports true.
func (c *carrierClient) until(what string, done func(record map[string]any) bool) map[string]any {
	c.t.Helper()
	timer := time.NewTimer(30 * time.Second)
	defer timer.Stop()
	for {
		select {
		case line, ok := <-c.lines:
			if !ok {
				c.t.Fatalf("the stream ended before %s", what)
			}
			c.seen = append(c.seen, line)
			var record map[string]any
			if err := json.Unmarshal([]byte(line), &record); err != nil {
				c.t.Fatalf("line %.200s: %v", line, err)
			}
			if done(record) {
				return record
			}
		case <-timer.C:
			c.t.Fatalf("%s timed out; %d lines read", what, len(c.seen))
		}
	}
}

func (c *carrierClient) request(line string) map[string]any {
	c.t.Helper()
	var head struct{ ID, Type string }
	if err := json.Unmarshal([]byte(line), &head); err != nil || head.ID == "" {
		c.t.Fatalf("request %s: %v", line, err)
	}
	c.send(line)
	return c.until("the "+head.Type+" reply", func(record map[string]any) bool {
		return record["type"] == "response" && record["id"] == head.ID && record["command"] == head.Type
	})
}

// carrierDrawn is what a client keeps of a snapshot: its number, the mount, the state, which content and its digest.
func carrierDrawn(seq uint64, s *uiview.Snapshot) string {
	return fmt.Sprintf("%d %s/%s/%d/%d %s alt=%d degraded=%v %s", seq, s.Plugin, s.Mount.Key, s.Mount.Epoch, s.Mount.Rev, s.State, s.Alternative, s.Degraded, s.Digest)
}

func carrierSnapshot(t *testing.T, value any) *uiview.Snapshot {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var s uiview.Snapshot
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatalf("snapshot %.300s: %v", raw, err)
	}
	return &s
}

// TestConformance_UIViewCarrier pins the wire carrier of D614 against the Go API in every SDK and on both line
// transports. A wire client and a Go client attach with the same abilities; the fixture runs the mount probe and the
// extras probe. The two clients must be sent the same sequence of numbers, mounts, states, alternatives and digests; an
// intent raised over the wire returns the extension's answer (the sum adds the host's limit); a wire client that stops
// reading is written exactly one ui_view_gap and its resync equals the Go client's; after a detach the next intent is
// stale. An SDK that drops a call fails by the probe lines, a carrier that drops or reorders a member by the sequence.
func TestConformance_UIViewCarrier(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	for _, tc := range sdkHarnessCases() {
		for _, transport := range []string{"pipe", "socket"} {
			t.Run(tc.name+"/"+transport, func(t *testing.T) {
				h := tc.make(t)
				t.Cleanup(func() {
					if h.cleanup != nil {
						h.cleanup()
					}
					if h.host != nil {
						h.host.Shutdown("test done")
					}
				})
				views := h.bridge.Views()
				abilities := extrasClientAbilities()
				var mu sync.Mutex
				var goClient []string
				goAtt, _, aerr := views.Attach(abilities, func(d uiview.Delivery) bool {
					mu.Lock()
					defer mu.Unlock()
					goClient = append(goClient, carrierDrawn(d.Seq, &d.Snapshot))
					return true
				})
				if aerr != nil {
					t.Fatal(aerr)
				}
				t.Cleanup(goAtt.Detach)
				goSent := func() []string {
					mu.Lock()
					defer mu.Unlock()
					return slices.Clone(goClient)
				}
				encoded, err := json.Marshal(abilities)
				if err != nil {
					t.Fatal(err)
				}
				c := serveCarrier(t, h.bridge, transport)
				attached := c.request(`{"id":"attach","type":"ui_view_attach","abilities":` + string(encoded) + `}`)
				data, _ := attached["data"].(map[string]any)
				if attached["success"] != true || data["seq"] != 0.0 || len(data["snapshots"].([]any)) != 0 {
					t.Fatalf("attach = %v", attached)
				}
				n := strconv.Itoa(int(data["attachment"].(float64)))

				pollUntilConformance(t, "abilities never replicated", func() bool {
					h.ui.ClearRecorded()
					runConformanceCommand(t, h, "ui_view_abilities")
					return slices.Contains(viewNotifies(h), "view-abilities:mounts=7,argbytes=123,slots=panel+status,nodes=text+box+select-list,markdown=true")
				})
				h.ui.ClearRecorded()
				runConformanceCommand(t, h, "ui_view_probe")
				waitViewNotify(t, h, "view-second:epoch=2,rev=1")
				runConformanceCommand(t, h, "ui_view_extras")
				waitViewNotify(t, h, "view-x-bigresult:invalid")
				want := []string{
					"view-mounted:epoch=1,rev=1,state=live",
					"view-updated:rev=2",
					"view-stale:stale",
					"view-hidden:hidden",
					"view-shown:live",
					"view-finished:finished",
					"view-terminal:terminal",
					"view-final:finished",
					"view-second:epoch=2,rev=1",
					"view-x-claims:colors=4242,kind=conf-kind,gestures=activate+drag,hover=true,panel.colors=77,profiles=scene,shapes=5,pixels=16",
					"view-x-mounted:live",
					"view-x-alts:invalid",
					"view-x-nest:invalid",
					"view-x-points:invalid",
					"view-x-shapes:invalid",
					"view-x-a11y:invalid",
					"view-x-over:limit",
					"view-x-pixels:unsupported",
					"view-x-stale:stale",
					"view-x-diag:invalid=5,limit=1,unsupported=1,stale=3",
					"view-x-bigresult:invalid",
				}
				if got := viewNotifies(h); !slices.Equal(got, want) {
					t.Fatalf("the probes reported\n  %q\nwant\n  %q", got, want)
				}

				// The wire client is sent what the Go client is sent: probe (mount, update, hide, show, finish), probe2 and
				// extras (the alternative the panel's claim selects), numbered alike and with the same digests.
				sent := goSent()
				if len(sent) != 7 {
					t.Fatalf("the Go client was sent %d snapshots, want 7:\n  %s", len(sent), strings.Join(sent, "\n  "))
				}
				var wireClient []string
				var second, extras *uiview.Snapshot
				for len(wireClient) < len(sent) {
					record := c.until("event "+strconv.Itoa(len(wireClient)+1), func(record map[string]any) bool { return record["type"] != "response" })
					if record["type"] != "ui_view" || record["attachment"] != data["attachment"] {
						t.Fatalf("event = %v", record)
					}
					s := carrierSnapshot(t, record["snapshot"])
					wireClient = append(wireClient, carrierDrawn(uint64(record["seq"].(float64)), s))
					switch s.Mount.Key {
					case "probe2":
						second = s
					case "extras":
						extras = s
					}
				}
				if !slices.Equal(wireClient, sent) {
					t.Fatalf("the wire client was sent\n  %s\nthe Go client\n  %s", strings.Join(wireClient, "\n  "), strings.Join(sent, "\n  "))
				}
				if second == nil || extras == nil || extras.Alternative != 1 || !strings.Contains(string(extras.View), "fixture-x-alt-77") || extras.Drawing == nil || len(extras.Drawing.Shapes) != 4 ||
					len(extras.A11y) != 1 || extras.A11y[0].Name != "Extras 77" || len(extras.Focus) != 1 || len(second.Intents) != 2 {
					t.Fatalf("the wire client's snapshots: probe2 %+v extras %+v", second, extras)
				}

				// An intent over the wire reaches the extension; the sum adds the host's argument limit.
				intent := func(id string, arg string) map[string]any {
					return c.request(fmt.Sprintf(`{"id":%q,"type":"ui_view_intent","attachment":%s,"plugin":%q,"mount":{"key":"probe2","epoch":%d,"rev":%d},"intent":"fx.add","arg":%s}`,
						id, n, tc.extName, second.Mount.Epoch, second.Mount.Rev, arg))
				}
				if r := intent("add", `{"n":20}`); fmt.Sprint(r["data"]) != "map[answer:map[sum:163] outcome:done ticket:"+fmt.Sprint(r["data"].(map[string]any)["ticket"])+"]" {
					t.Errorf("fx.add over the wire = %v", r)
				}
				if r := intent("bad", `{"n":"x"}`); r["data"].(map[string]any)["outcome"] != "refused" {
					t.Errorf("fx.add with a string over the wire = %v", r)
				}

				// A forced gap: the wire client stops reading while another plugin's mount changes 6000 times. The Go
				// client takes every delivery; the wire client is written a run of them, one ui_view_gap, and nothing more.
				c.gate.Lock()
				ref, merr := views.Mount("other", mustMount(t, "o", "other-0"))
				if merr != nil {
					c.gate.Unlock()
					t.Fatal(merr)
				}
				for i := 1; i <= 6000; i++ {
					update, derr := uiview.DecodeUpdate(fmt.Appendf(nil, `{"mount":{"key":"o","epoch":%d,"rev":%d},"view":{"root":{"kind":"text","text":"other-%d"}}}`, ref.Mount.Epoch, ref.Mount.Rev, i))
					if derr != nil {
						c.gate.Unlock()
						t.Fatal(derr)
					}
					if ref, merr = views.Update("other", update); merr != nil {
						c.gate.Unlock()
						t.Fatal(merr)
					}
				}
				c.gate.Unlock()
				last := 7.0
				gap := c.until("the gap", func(record map[string]any) bool {
					if record["type"] == "ui_view" {
						if record["seq"] != last+1 {
							t.Fatalf("event %v follows %v", record["seq"], last)
						}
						last++
					}
					return record["type"] != "ui_view"
				})
				if gap["type"] != "ui_view_gap" || gap["attachment"] != data["attachment"] || gap["seq"] != last+1 || last < 7+256 || last >= 6008 {
					t.Fatalf("after event %v the wire client was written %v", last, gap)
				}
				if got := goSent(); len(got) != 6008 {
					t.Fatalf("the Go client was sent %d snapshots, want 6008", len(got))
				}
				// In the gap the host writes nothing for the attachment; the next line is the resync's reply, and its
				// snapshots are the Go client's state.
				if _, merr = views.Update("other", mustUpdate(t, ref.Mount, "other-last")); merr != nil {
					t.Fatal(merr)
				}
				c.send(`{"id":"rs","type":"ui_view_resync","attachment":` + n + `}`)
				resync := c.until("the resync reply", func(map[string]any) bool { return true })
				rs, _ := resync["data"].(map[string]any)
				if resync["id"] != "rs" || resync["success"] != true || rs["seq"] != gap["seq"] {
					t.Fatalf("after the gap the wire client was written %.300v", resync)
				}
				goState, serr := goAtt.Resync()
				if serr != nil {
					t.Fatal(serr)
				}
				var wireState, goWant []string
				for _, value := range rs["snapshots"].([]any) {
					wireState = append(wireState, carrierDrawn(0, carrierSnapshot(t, value)))
				}
				for i := range goState.Snapshots {
					goWant = append(goWant, carrierDrawn(0, &goState.Snapshots[i]))
				}
				if len(goWant) != 4 || !slices.Equal(wireState, goWant) {
					t.Fatalf("the resync returned\n  %s\nthe Go client's state is\n  %s", strings.Join(wireState, "\n  "), strings.Join(goWant, "\n  "))
				}
				// The numbers go on from the resync's.
				if _, merr = views.Update("other", mustUpdate(t, uiview.Ref{Key: "o", Epoch: ref.Mount.Epoch, Rev: ref.Mount.Rev + 1}, "other-after")); merr != nil {
					t.Fatal(merr)
				}
				if next := c.until("the event after the resync", func(map[string]any) bool { return true }); next["type"] != "ui_view" || next["seq"] != gap["seq"].(float64)+1 {
					t.Fatalf("after the resync the wire client was written %.300v", next)
				}

				// Detach: the next intent is stale and never reaches the extension.
				if r := c.request(`{"id":"detach","type":"ui_view_detach","attachment":` + n + `}`); r["success"] != true {
					t.Fatalf("detach = %v", r)
				}
				if r := intent("late", `{"n":1}`); r["data"].(map[string]any)["outcome"] != "stale" {
					t.Errorf("an intent after the detach = %v", r)
				}
			})
		}
	}
}

func mustUpdate(t *testing.T, ref uiview.Ref, text string) uiview.UpdateArgs {
	t.Helper()
	update, err := uiview.DecodeUpdate(fmt.Appendf(nil, `{"mount":{"key":%q,"epoch":%d,"rev":%d},"view":{"root":{"kind":"text","text":%q}}}`, ref.Key, ref.Epoch, ref.Rev, text))
	if err != nil {
		t.Fatal(err)
	}
	return update
}

// passGate returns once the gate is free: the reader goroutine stops here while the test holds the gate.
func (c *carrierClient) passGate() {
	c.gate.Lock()
	defer c.gate.Unlock()
}
