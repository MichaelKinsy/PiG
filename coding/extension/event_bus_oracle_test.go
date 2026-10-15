package extension

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"os/exec"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

type eventBusOp struct {
	Op       string `json:"op"`
	ID       string `json:"id,omitempty"`
	Channel  string `json:"channel,omitempty"`
	Behavior string `json:"behavior,omitempty"`
	Nested   string `json:"nested,omitempty"`
	Data     any    `json:"data"`
}

type eventBusScenario struct {
	Ops []eventBusOp `json:"ops"`
}

type eventBusOut struct {
	Calls  []string `json:"calls"`
	Thrown *string  `json:"thrown"`
	Logged []string `json:"logged"`
}

func eventBusScenarios() []eventBusScenario {
	rng := rand.New(rand.NewPCG(3, 61))
	pick := func(items []string) string { return items[rng.IntN(len(items))] }
	// Node's EventEmitter treats the channels error, newListener and removeListener specially (an emit of "error" with no listener throws, subscribing announces itself on "newListener"); Pig's bus does not (Q15), so the scenarios use ordinary channels.
	channels := []string{"a", "b", "c", "a:b"}
	datas := []any{nil, 1, "text", map[string]any{"k": []any{1, 2}}, []any{}, true, "日本語", 1.5}
	var scenarios []eventBusScenario
	for range 600 {
		// A nested emit goes to a channel only the sink listens on, so handlers cannot recurse without end.
		scenario := eventBusScenario{Ops: []eventBusOp{{Op: "on", ID: "sink", Channel: "sink", Behavior: "record"}}}
		ids := 0
		for range 3 + rng.IntN(14) {
			switch rng.IntN(8) {
			case 0, 1, 2:
				ids++
				scenario.Ops = append(scenario.Ops, eventBusOp{Op: "on", ID: fmt.Sprintf("h%d", ids), Channel: pick(channels), Nested: "sink", Behavior: pick([]string{"record", "record", "record", "throw", "unsubscribeSelf", "subscribeNew", "clear", "emitNested"})})
			case 3:
				scenario.Ops = append(scenario.Ops, eventBusOp{Op: "off", ID: fmt.Sprintf("h%d", 1+rng.IntN(max(1, ids)))})
			case 4:
				scenario.Ops = append(scenario.Ops, eventBusOp{Op: "clear"})
			default:
				scenario.Ops = append(scenario.Ops, eventBusOp{Op: "emit", Channel: pick(channels), Data: datas[rng.IntN(len(datas))]})
			}
		}
		scenarios = append(scenarios, scenario)
	}
	return scenarios
}

func runEventBusWithPig(t *testing.T, scenario eventBusScenario) []eventBusOut {
	bus := CreateEventBus()
	unsubscribe := map[string]func(){}
	var calls []string
	var outs []eventBusOut
	loggedPattern := regexp.MustCompile(`^(Event handler error \([^)]*\))`)
	for _, op := range scenario.Ops {
		calls = nil
		reader, writer, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		saved := os.Stderr
		os.Stderr = writer
		var thrown *string
		func() {
			defer func() {
				if recovered := recover(); recovered != nil {
					text := fmt.Sprint(recovered)
					thrown = &text
				}
			}()
			switch op.Op {
			case "on":
				id, channel := op.ID, op.Channel
				unsubscribe[id] = bus.On(channel, func(data any) {
					raw, _ := json.Marshal(data)
					calls = append(calls, fmt.Sprintf("%s:%s", id, raw))
					switch op.Behavior {
					case "throw":
						panic("boom")
					case "unsubscribeSelf":
						if off := unsubscribe[id]; off != nil {
							off()
						}
					case "subscribeNew":
						unsubscribe[id+"n"] = bus.On(channel, func(any) { calls = append(calls, fmt.Sprintf("%sn:%s", id, raw)) })
					case "clear":
						bus.Clear()
					case "emitNested":
						bus.Emit(op.Nested, "nested")
					}
				})
			case "off":
				if off := unsubscribe[op.ID]; off != nil {
					off()
				}
			case "emit":
				bus.Emit(op.Channel, op.Data)
			case "clear":
				bus.Clear()
			}
		}()
		os.Stderr = saved
		_ = writer.Close()
		captured, _ := io.ReadAll(reader)
		logged := []string{}
		for line := range strings.SplitSeq(strings.TrimSpace(string(captured)), "\n") {
			if match := loggedPattern.FindStringSubmatch(line); match != nil {
				logged = append(logged, match[1])
			}
		}
		outs = append(outs, eventBusOut{Calls: append([]string{}, calls...), Thrown: thrown, Logged: logged})
	}
	return outs
}

// createEventBus (event-bus.ts) against pinned Pi over 600 seeded sequences of subscribe, unsubscribe, emit and clear: handlers run in registration order with the emitted data, a handler that throws is reported as `Event handler error (<channel>)` without reaching the emitter or the later handlers, a handler that unsubscribes itself, subscribes to the same channel, clears the bus or emits on another channel during an emit changes later emits but not the one in flight, and unsubscribing twice or an unknown handler is harmless.
func TestEventBusMatchesPi(t *testing.T) {
	scenarios := eventBusScenarios()
	input, err := json.Marshal(scenarios)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/event_bus.mjs", pigversion.UpstreamPackageVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected [][]eventBusOut
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	failures := 0
	for i, scenario := range scenarios {
		got := runEventBusWithPig(t, scenario)
		raw, _ := json.Marshal(got)
		var normalized []eventBusOut
		_ = json.Unmarshal(raw, &normalized)
		if reflect.DeepEqual(normalized, expected[i]) {
			continue
		}
		if failures++; failures > 5 {
			continue
		}
		for k := range scenario.Ops {
			if !reflect.DeepEqual(normalized[k], expected[i][k]) {
				ops, _ := json.Marshal(scenario.Ops[:k+1])
				g, _ := json.Marshal(normalized[k])
				e, _ := json.Marshal(expected[i][k])
				t.Errorf("scenario %d\n  ops %.900s\n  Pig %s\n  Pi  %s", i, ops, g, e)
				break
			}
		}
	}
	if failures > 5 {
		t.Errorf("%d of %d scenarios differ from Pi", failures, len(scenarios))
	}
}
