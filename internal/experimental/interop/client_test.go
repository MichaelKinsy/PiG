//go:build !windows

package interop

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/MichaelKinsy/PiG/internal/chord"
	"github.com/MichaelKinsy/PiG/internal/experimental/client"
	"github.com/MichaelKinsy/PiG/internal/experimental/protocol"
)

// Ports packages/client/src/{client,connection,unix}.ts and packages/server/src/{server,session-router}.ts and
// transports/unix/*.ts as a four-way differential. Each client (the Go client and the pinned Node client, running
// testdata/client-scenario.mjs) drives each server (the Go server and the pinned Node server running the echo host);
// every pairing must produce the transcript of the pinned Node client against the pinned Node server, and each server
// must observe the same host events.

type step = map[string]any

// failure mirrors the oracle's { name, code, message } for a client-side error.
func failure(err error) map[string]any {
	name, code := "Error", any(nil)
	if serverError, ok := errors.AsType[*client.ServerError](err); ok {
		name, code = "ServerError", serverError.Code
	} else if _, ok := errors.AsType[*client.DisconnectedError](err); ok {
		name = "DisconnectedError"
	} else if _, ok := errors.AsType[*client.ClientDisposedError](err); ok {
		name = "ClientDisposedError"
	} else if _, ok := errors.AsType[*protocol.ProtocolValidationError](err); ok {
		name = "ProtocolValidationError"
	} else if errors.Is(err, context.Canceled) {
		name = "AbortError"
	}
	return map[string]any{"name": name, "code": code, "message": err.Error()}
}

type goScenario struct {
	t          *testing.T
	client     *client.Client
	transcript []step
}

func (s *goScenario) record(name string, outcome step) {
	outcome["name"] = name
	s.transcript = append(s.transcript, outcome)
}

// step runs one scenario step; value undefined (nil) maps to resultUndefined.
func (s *goScenario) step(name string, run func() (any, error)) {
	value, err := run()
	switch {
	case err != nil:
		s.record(name, step{"error": failure(err)})
	case value == nil:
		s.record(name, step{"resultUndefined": true})
	default:
		s.record(name, step{"result": value})
	}
}

func (s *goScenario) request(target protocol.RpcTarget, call chord.ServiceCall) (any, error) {
	raw, err := s.client.Request(s.t.Context(), target, call)
	if err != nil {
		return nil, err
	}
	if raw == nil {
		return nil, nil
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, err
	}
	if value == nil {
		return jsonNull{}, nil
	}
	return value, nil
}

// jsonNull is a present null result, distinct from an absent one.
type jsonNull struct{}

func (jsonNull) MarshalJSON() ([]byte, error) { return []byte("null"), nil }

func args(values ...string) []json.RawMessage {
	out := make([]json.RawMessage, len(values))
	for i, value := range values {
		out[i] = json.RawMessage(value)
	}
	return out
}

func (s *goScenario) waitFor(predicate func() bool) {
	for range 2000 {
		if predicate() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// generic re-expresses a value as decoded JSON, the form the Node scenario prints.
func generic(value any) any {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	var out any
	if err := json.Unmarshal(encoded, &out); err != nil {
		panic(err)
	}
	return out
}

// stream is the Go twin of the Node scenario's "stream" step: subscribe, mutate through four calls, observe the ordered updates.
func (s *goScenario) stream(session protocol.SessionTarget) (any, error) {
	var mu sync.Mutex
	var updates []any
	subscription, err := s.client.SubscribeService(s.t.Context(), session, "interop.counter", chord.ServiceSingleton, func(update chord.ServiceProviderUpdate) error {
		mu.Lock()
		updates = append(updates, generic(update))
		mu.Unlock()
		return nil
	})
	if err != nil {
		return nil, err
	}
	snapshot := generic(subscription.Snapshot)
	subscription.Start()
	for _, call := range []struct {
		member string
		arg    string
	}{{"bump", "1"}, {"bump", "2"}, {"rename", `"é😀"`}, {"bump", "4"}} {
		if _, err := s.client.Request(s.t.Context(), session, chord.ServiceCall{ServiceId: "interop.counter", Member: call.member, Args: args(call.arg)}); err != nil {
			return nil, err
		}
	}
	s.waitFor(func() bool { mu.Lock(); defer mu.Unlock(); return len(updates) >= 4 })
	if err := subscription.Dispose(); err != nil {
		return nil, err
	}
	if _, err := s.client.Request(s.t.Context(), session, chord.ServiceCall{ServiceId: "interop.counter", Member: "bump", Args: args("100")}); err != nil {
		return nil, err
	}
	time.Sleep(150 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	return step{"subscriptionId": subscription.Id, "snapshot": snapshot, "updates": updates, "afterDispose": float64(len(updates))}, nil
}

func attachmentSummary(attachment *protocol.SessionTarget) any {
	if attachment == nil {
		return nil
	}
	return map[string]any{"serverId": attachment.ServerId, "sessionId": attachment.SessionId, "attachmentId": attachment.AttachmentId != ""}
}

// runGoClient is the Go twin of testdata/client-scenario.mjs; keep the two step lists identical.
func runGoClient(t *testing.T, path string) []step {
	t.Helper()
	factory, err := client.CreateUnixTransportFactory(client.UnixTransportOptions{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	c, err := client.NewClient(client.ClientOptions{TransportFactory: factory, ServerId: logicalServerID})
	if err != nil {
		t.Fatal(err)
	}
	s := &goScenario{t: t, client: c}
	server := protocol.ServerTarget{ServerId: logicalServerID}
	management := func(member string, arguments ...string) chord.ServiceCall {
		return chord.ServiceCall{ServiceId: "pi.session-management", Member: member, Args: args(arguments...)}
	}
	s.step("connect", func() (any, error) {
		hello, err := c.Connect(t.Context())
		if err != nil {
			return nil, err
		}
		return step{"version": float64(hello.Version), "serverId": hello.ServerId, "connected": c.Connected()}, nil
	})
	catalogue := func(target protocol.RpcTarget) (any, error) {
		_, err := c.ServiceCatalogue(t.Context(), target)
		return nil, err
	}
	s.step("catalogue-server", func() (any, error) { return catalogue(server) })
	s.step("attach-unknown", func() (any, error) { return s.request(server, management("attach", `"nope"`)) })
	s.step("attach-bad-arguments", func() (any, error) { return s.request(server, management("attach", `1`)) })
	s.step("server-call-unsupported", func() (any, error) {
		return s.request(server, chord.ServiceCall{ServiceId: "x", Member: "y", Args: args()})
	})
	s.step("wrong-server", func() (any, error) {
		return s.request(protocol.ServerTarget{ServerId: otherServerID}, management("detach"))
	})
	s.step("attach", func() (any, error) {
		result, err := s.request(server, management("attach", `"session-1"`))
		if err != nil {
			return nil, err
		}
		s.waitFor(func() bool { return c.Attachment() != nil })
		return step{"result": result, "attachment": attachmentSummary(c.Attachment())}, nil
	})
	session := *c.Attachment()
	echo := func(member string, arguments ...string) chord.ServiceCall {
		return chord.ServiceCall{ServiceId: "s", Member: member, Args: args(arguments...)}
	}
	rich := chord.ServiceCall{ServiceId: "svc", Member: "echo", Instance: &chord.ServiceInstanceAddress{Key: "i-1", Generation: 2}, Args: args(
		`null`, `true`, `false`, `0`, `1`, `-1`, `1.5`, `-2.25`, `9007199254740991`, `-9007199254740991`, `1e-7`,
		`"é😀\u0000\u001f"`, `""`, `[]`, `{}`, `{"b":1,"a":2,"10":3,"2":4,"nested":[{"k":[1,[2,[3]]]}]}`)}
	s.step("stream", func() (any, error) { return s.stream(session) })
	s.step("stream-unknown-service", func() (any, error) {
		_, err := c.SubscribeService(t.Context(), session, "interop.missing", chord.ServiceSingleton, func(chord.ServiceProviderUpdate) error { return nil })
		return nil, err
	})
	s.step("catalogue-session", func() (any, error) {
		entries, err := c.ServiceCatalogue(t.Context(), session)
		return generic(entries), err
	})
	s.step("echo", func() (any, error) { return s.request(session, rich) })
	s.step("echo-unsafe-integer", func() (any, error) { return s.request(session, echo("echo", `1e21`)) })
	s.step("echo-empty-args", func() (any, error) { return s.request(session, echo("echo")) })
	s.step("undefined-result", func() (any, error) { return s.request(session, echo("undefined")) })
	s.step("null-result", func() (any, error) { return s.request(session, echo("null")) })
	s.step("service-error", func() (any, error) { return s.request(session, echo("fail")) })
	s.step("internal-error", func() (any, error) { return s.request(session, echo("secret")) })
	s.step("stale-attachment", func() (any, error) {
		stale := session
		stale.AttachmentId = "bogus"
		return s.request(stale, echo("echo"))
	})
	s.step("unattached-session", func() (any, error) {
		return s.request(protocol.SessionTarget{ServerId: logicalServerID, SessionId: "session-2", AttachmentId: "bogus"}, echo("echo"))
	})
	s.step("large-result", func() (any, error) {
		raw, err := s.client.Request(t.Context(), session, echo("big"))
		if err != nil {
			return nil, err
		}
		var decoded struct{ Data string }
		if err := json.Unmarshal(raw, &decoded); err != nil {
			return nil, err
		}
		runes := []rune(decoded.Data)
		return step{"echo": nil, "length": utf16Length(decoded.Data), "tail": string(runes[len(runes)-2:])}, nil
	})
	s.step("large-argument", func() (any, error) {
		text := strings.Repeat("y", 2<<20) + "😀"
		encoded, _ := json.Marshal(text)
		raw, err := s.client.Request(t.Context(), session, echo("echo", string(encoded)))
		if err != nil {
			return nil, err
		}
		var decoded struct{ Echo struct{ Args []string } }
		if err := json.Unmarshal(raw, &decoded); err != nil {
			return nil, err
		}
		return step{"length": utf16Length(decoded.Echo.Args[0]), "equal": decoded.Echo.Args[0] == text}, nil
	})
	s.step("pipelined", func() (any, error) {
		operations := make([]*chord.ServiceInvocation, 50)
		for i := range operations {
			operation, err := c.BeginInvoke(t.Context(), session, echo("echo", strconv.Itoa(i)))
			if err != nil {
				return nil, err
			}
			operations[i] = operation
		}
		results := make([]any, len(operations))
		for i, operation := range operations {
			raw, err := operation.Wait(t.Context())
			if err != nil {
				return nil, err
			}
			var decoded struct{ Echo struct{ Args []float64 } }
			if err := json.Unmarshal(raw, &decoded); err != nil {
				return nil, err
			}
			results[i] = decoded.Echo.Args[0]
		}
		return results, nil
	})
	s.step("cancel", func() (any, error) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		operation, err := c.BeginInvoke(ctx, session, echo("hang"))
		if err != nil {
			return nil, err
		}
		cancel()
		outcome := "resolved"
		if _, err := operation.Wait(context.Background()); err != nil {
			outcome = "rejected:" + failure(err)["name"].(string)
		}
		after, err := s.request(session, echo("echo", `"after-cancel"`))
		if err != nil {
			return nil, err
		}
		return step{"outcome": outcome, "after": after.(map[string]any)["echo"].(map[string]any)["args"].([]any)[0]}, nil
	})
	s.step("detach", func() (any, error) {
		result, err := s.request(server, management("detach"))
		if err != nil {
			return nil, err
		}
		s.waitFor(func() bool { return c.Attachment() == nil })
		return step{"result": result, "attachment": attachmentSummary(c.Attachment())}, nil
	})
	s.step("after-detach", func() (any, error) { return s.request(session, echo("echo")) })
	s.step("reattach", func() (any, error) {
		if _, err := s.request(server, management("attach", `"session-2"`)); err != nil {
			return nil, err
		}
		s.waitFor(func() bool { return c.Attachment() != nil && c.Attachment().SessionId == "session-2" })
		return step{"sessionId": c.Attachment().SessionId}, nil
	})
	s.step("disconnect", func() (any, error) {
		c.Disconnect("Client disconnected")
		return step{"state": string(c.ConnectionState()), "attachment": attachmentSummary(c.Attachment())}, nil
	})
	s.step("request-while-disconnected", func() (any, error) { return s.request(server, management("detach")) })
	s.step("reconnect", func() (any, error) {
		hello, err := c.Connect(t.Context())
		if err != nil {
			return nil, err
		}
		return step{"serverId": hello.ServerId, "attachment": attachmentSummary(c.Attachment())}, nil
	})
	s.step("dispose", func() (any, error) {
		if err := c.Dispose(); err != nil {
			return nil, err
		}
		if err := c.WaitClosed(t.Context()); err != nil {
			return nil, err
		}
		return step{"disposed": true}, nil
	})
	s.step("request-disposed", func() (any, error) {
		return s.request(server, chord.ServiceCall{ServiceId: "x", Member: "y", Args: args()})
	})
	return s.transcript
}

func utf16Length(text string) float64 { return float64(len(utf16.Encode([]rune(text)))) }

// runNodeClient runs the pinned packages/client scenario and returns its transcript.
func runNodeClient(t *testing.T, path string) []step {
	t.Helper()
	cmd := nodeScript(t.Context(), t, "client-scenario.mjs", path, logicalServerID, otherServerID)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Node client: %v\n%s", err, stderr.String())
	}
	var transcript []step
	if err := json.Unmarshal(output, &transcript); err != nil {
		t.Fatalf("Node client transcript: %v\n%s", err, output)
	}
	return transcript
}

// normalizeTranscript round-trips through JSON so numbers and nil values compare as the oracle printed them.
func normalizeTranscript(t *testing.T, transcript []step) []step {
	t.Helper()
	encoded, err := json.Marshal(transcript)
	if err != nil {
		t.Fatal(err)
	}
	var out []step
	if err := json.Unmarshal(encoded, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func diffTranscripts(t *testing.T, label string, got, want []step) {
	t.Helper()
	var bad mismatches
	for i := range max(len(got), len(want)) {
		switch {
		case i >= len(got):
			bad.add("%s: missing step %v", label, want[i]["name"])
		case i >= len(want):
			bad.add("%s: extra step %v", label, got[i]["name"])
		case !reflect.DeepEqual(got[i], want[i]):
			g, _ := json.Marshal(got[i])
			w, _ := json.Marshal(want[i])
			bad.add("%s: step %v\n  got:  %.400s\n  want: %.400s", label, want[i]["name"], g, w)
		}
	}
	bad.report(t, len(want))
}

// Pi: packages/client/src/client.ts:213 (id)
// Pi: packages/client/src/client.ts:191 (snapshot)
// packages/client/src/types.ts:16-19: a ServiceSubscription has an id, a target and the snapshot the server returned.
func TestClientServerInterop(t *testing.T) {
	t.Parallel()
	// The oracle: the pinned Node client against the pinned Node server.
	oracleServer := startNodeServer(t, nil)
	oracle := normalizeTranscript(t, runNodeClient(t, oracleServer.path))
	oracleServer.stop()
	oracleEvents := oracleServer.log.snapshot()
	for _, step := range oracle {
		if step["name"] == "connect" {
			if _, ok := step["result"]; !ok {
				t.Fatalf("oracle did not connect: %v", step)
			}
		}
	}
	for _, server := range []struct {
		name  string
		start func(*testing.T, *float64) *serverFixture
	}{{"go server", startGoServer}, {"node server", startNodeServer}} {
		for _, clientKind := range []string{"go client", "node client"} {
			if server.name == "node server" && clientKind == "node client" {
				continue // the oracle itself
			}
			t.Run(clientKind+" to "+server.name, func(t *testing.T) {
				t.Parallel()
				fixture := server.start(t, nil)
				var transcript []step
				if clientKind == "go client" {
					transcript = runGoClient(t, fixture.path)
				} else {
					transcript = runNodeClient(t, fixture.path)
				}
				fixture.stop()
				diffTranscripts(t, "transcript", normalizeTranscript(t, transcript), oracle)
				if got := fixture.log.snapshot(); !slicesEqual(got, oracleEvents) {
					t.Fatalf("server events differ from the pinned Node pair:\n got: %s\nwant: %s", strings.Join(got, "\n      "), strings.Join(oracleEvents, "\n      "))
				}
			})
		}
	}
}

func slicesEqual(a, b []string) bool {
	return len(a) == len(b) && strings.Join(a, "\x00") == strings.Join(b, "\x00")
}
