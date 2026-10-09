// SPDX-License-Identifier: MIT

package driver_test

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/core/abi"
	"github.com/MichaelKinsy/PiG/durable/core/abi/payload"
	"github.com/MichaelKinsy/PiG/durable/core/driver"
	"github.com/MichaelKinsy/PiG/durable/core/sqlhost"
)

// funcCore is a core that is a function: the test says what each event produces.
type funcCore struct {
	mu      sync.Mutex
	respond func(abi.Event) *abi.Step
	seen    []abi.Event
}

func (c *funcCore) Step(ev abi.Event) *abi.Step {
	c.mu.Lock()
	defer c.mu.Unlock()
	ev.Payload = append([]byte(nil), ev.Payload...)
	c.seen = append(c.seen, ev)
	if c.respond == nil {
		return &abi.Step{}
	}
	if s := c.respond(ev); s != nil {
		return s
	}
	return &abi.Step{}
}

func (c *funcCore) events(kind abi.EventKind) []abi.Event {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []abi.Event
	for _, e := range c.seen {
		if e.Kind == kind {
			out = append(out, e)
		}
	}
	return out
}

func effect(id uint32, kind abi.EffectKind, v any) abi.Effect {
	body, _ := json.Marshal(v)
	return abi.Effect{ID: id, Kind: kind, Payload: body}
}

func fauxModels(config ai.FauxConfig, responses ...ai.FauxResponseStep) durable.Models {
	config.ProviderID = "probe"
	config.Models = []ai.FauxModelDefinition{{ID: "probe-1", ContextWindow: 100_000, MaxTokens: 1000}}
	faux := ai.NewFauxProvider(config)
	faux.SetResponses(responses)
	models := ai.CreateModels()
	models.SetProvider(faux.Provider())
	return models
}

func start(t *testing.T, core *funcCore, models durable.Models) *sqlhost.Host {
	t.Helper()
	db, err := sqlhost.OpenDB(t.Context(), filepath.Join(t.TempDir(), "s.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	host := sqlhost.New(sqlhost.Options{
		Core: core, DB: db, OwnDB: true, Clock: func() float64 { return 5000 },
		Handlers: driver.NewModels(models, func() float64 { return 5000 }, nil).Handlers(),
	})
	t.Cleanup(func() { _ = host.Close(context.Background()) })
	return host
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

var probeModel = payload.ModelRef{Provider: "probe", ModelID: "probe-1"}

const userMessage = `{"role":"user","content":"hi","timestamp":1}`

// types returns the event types of every model_event payload the core received, in order.
func types(core *funcCore) []string {
	var out []string
	for _, ev := range core.events(abi.EventModelEvent) {
		var batch []struct{ Type string }
		_ = json.Unmarshal(ev.Payload, &batch)
		for _, e := range batch {
			out = append(out, e.Type)
		}
	}
	return out
}

func TestModelContextStreamsAFauxResponseEndingInDone(t *testing.T) {
	core := &funcCore{respond: func(ev abi.Event) *abi.Step {
		if ev.Kind == abi.EventOpen {
			return &abi.Step{Effects: []abi.Effect{effect(1, abi.EffectModelContext, payload.ModelContextEffect{Model: probeModel, Context: json.RawMessage(`{"messages":[` + userMessage + `]}`)})}}
		}
		return nil
	}}
	host := start(t, core, fauxModels(ai.FauxConfig{}, ai.FauxAssistantMessage(ai.FauxContentBlocks{ai.FauxText("hello from the faux provider")}, ai.FauxAssistantMessageOptions{})))
	if err := host.Send(t.Context(), abi.Event{Kind: abi.EventOpen, Payload: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "a done event", func() bool { got := types(core); return len(got) > 0 && got[len(got)-1] == "done" })
	got := types(core)
	if got[0] != "start" {
		t.Fatalf("events = %v", got)
	}
	last := core.events(abi.EventModelEvent)
	var batch []struct {
		Type    string
		Message struct{ Content []struct{ Text string } }
	}
	if err := json.Unmarshal(last[len(last)-1].Payload, &batch); err != nil {
		t.Fatal(err)
	}
	if text := batch[0].Message.Content[0].Text; text != "hello from the faux provider" {
		t.Fatalf("final message text = %q", text)
	}
	if id := last[len(last)-1].ID; id != 1 {
		t.Fatalf("events carry the effect ID, got %d", id)
	}
}

func TestDeltaFormExtendsTheKeptListInPlace(t *testing.T) {
	var mu sync.Mutex
	var sizes []int
	factory := func(request ai.TranscriptContext, _ ai.StreamOptions, _ *ai.FauxProviderState, _ *ai.Model) (ai.AssistantMessage, error) {
		mu.Lock()
		sizes = append(sizes, len(request.Messages()))
		mu.Unlock()
		return ai.FauxAssistantMessage(ai.FauxContentBlocks{ai.FauxText("ok")}, ai.FauxAssistantMessageOptions{}), nil
	}
	var asked int
	core := &funcCore{respond: func(ev abi.Event) *abi.Step {
		switch ev.Kind {
		case abi.EventOpen:
			return &abi.Step{Effects: []abi.Effect{effect(1, abi.EffectModelContext, payload.ModelContextEffect{Model: probeModel, Context: json.RawMessage(`{"messages":[` + userMessage + `]}`)})}}
		case abi.EventModelEvent:
			if strings.Contains(string(ev.Payload), `"type":"done"`) && ev.ID == 1 && asked == 0 {
				asked++
				one := uint32(1)
				return &abi.Step{Effects: []abi.Effect{effect(2, abi.EffectModelContext, payload.ModelContextEffect{Model: probeModel, Extends: &one, Append: json.RawMessage(`[{"role":"user","content":"more","timestamp":2}]`)})}}
			}
		}
		return nil
	}}
	host := start(t, core, fauxModels(ai.FauxConfig{}, ai.FauxFactoryStep(factory), ai.FauxFactoryStep(factory)))
	if err := host.Send(t.Context(), abi.Event{Kind: abi.EventOpen, Payload: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "two requests", func() bool { mu.Lock(); defer mu.Unlock(); return len(sizes) == 2 })
	if sizes[0] != 1 || sizes[1] != 2 {
		t.Fatalf("message counts per request = %v, want [1 2]", sizes)
	}
}

func TestUnknownExtendsDiscardsTheHost(t *testing.T) {
	core := &funcCore{respond: func(ev abi.Event) *abi.Step {
		if ev.Kind == abi.EventOpen {
			ghost := uint32(41)
			return &abi.Step{Effects: []abi.Effect{effect(1, abi.EffectModelContext, payload.ModelContextEffect{Model: probeModel, Extends: &ghost, Append: json.RawMessage(`[]`)})}}
		}
		return nil
	}}
	host := start(t, core, fauxModels(ai.FauxConfig{}))
	if err := host.Send(t.Context(), abi.Event{Kind: abi.EventOpen, Payload: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the host to be discarded", func() bool { return host.Err() != nil })
	var fe *sqlhost.FatalError
	if !errors.As(host.Err(), &fe) || !strings.Contains(host.Err().Error(), "extends effect 41") {
		t.Fatalf("err = %v", host.Err())
	}
}

func TestAnUnavailableModelIsAnErrorEventTheCoreClassifies(t *testing.T) {
	core := &funcCore{respond: func(ev abi.Event) *abi.Step {
		if ev.Kind == abi.EventOpen {
			return &abi.Step{Effects: []abi.Effect{effect(1, abi.EffectModelContext, payload.ModelContextEffect{Model: payload.ModelRef{Provider: "none", ModelID: "x"}, Context: json.RawMessage(`{"messages":[` + userMessage + `]}`)})}}
		}
		return nil
	}}
	host := start(t, core, fauxModels(ai.FauxConfig{}))
	if err := host.Send(t.Context(), abi.Event{Kind: abi.EventOpen, Payload: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "an error event", func() bool { return len(core.events(abi.EventModelEvent)) == 1 })
	var batch []struct {
		Type  string
		Error struct{ StopReason, ErrorMessage string }
	}
	if err := json.Unmarshal(core.events(abi.EventModelEvent)[0].Payload, &batch); err != nil {
		t.Fatal(err)
	}
	if batch[0].Type != "error" || batch[0].Error.StopReason != "error" || !strings.Contains(batch[0].Error.ErrorMessage, "none/x is not available") {
		t.Fatalf("event = %+v", batch[0])
	}
	if host.Err() != nil {
		t.Fatalf("a missing model is data, not a failure: %v", host.Err())
	}
}

func TestCancelStopsAStreamingResponse(t *testing.T) {
	long := ai.FauxAssistantMessage(ai.FauxContentBlocks{ai.FauxText(strings.Repeat("word ", 400))}, ai.FauxAssistantMessageOptions{})
	cancelled := false
	core := &funcCore{respond: func(ev abi.Event) *abi.Step {
		switch ev.Kind {
		case abi.EventOpen:
			return &abi.Step{Effects: []abi.Effect{effect(1, abi.EffectModelContext, payload.ModelContextEffect{Model: probeModel, Context: json.RawMessage(`{"messages":[` + userMessage + `]}`)})}}
		case abi.EventAbort:
			cancelled = true
			return &abi.Step{Effects: []abi.Effect{{ID: 2, Kind: abi.EffectCancel, Payload: abi.U32Payload(1)}}}
		}
		return nil
	}}
	host := start(t, core, fauxModels(ai.FauxConfig{TokensPerSecond: 50, TokenSize: &ai.FauxTokenSize{Min: new(1), Max: new(1)}}, long))
	if err := host.Send(t.Context(), abi.Event{Kind: abi.EventOpen, Payload: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the stream to start", func() bool { return len(core.events(abi.EventModelEvent)) > 0 })
	if err := host.Send(t.Context(), abi.Event{Kind: abi.EventAbort, Payload: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the cancel to be sent", func() bool { return cancelled })
	before := len(core.events(abi.EventModelEvent))
	time.Sleep(150 * time.Millisecond)
	if after := len(core.events(abi.EventModelEvent)); after > before+2 {
		t.Fatalf("events kept flowing after cancel: %d then %d", before, after)
	}
	if host.Err() != nil {
		t.Fatalf("cancel must not discard the host: %v", host.Err())
	}
}
