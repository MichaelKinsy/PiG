package subprocess

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// sharedDelivery is one message_update frame an extension was sent, with the JSON the whole event encoded to when it was prepared.
type sharedDelivery struct {
	extension int
	prepared  preparedMessageUpdate
	whole     []byte
}

// The host prepares one message_update payload per publication and hands it to every subscribed extension (messageUpdateShare): with three extensions, each delta of a streamed response is prepared once. Each extension still holds the object the whole event encodes to when it receives it. When the partial publishes between two extensions' deliveries, as it does while an extension's handler awaits its process, the later extensions' payload is prepared again and carries the new state, as an unshared payload would.
func TestMessageUpdatePayloadIsSharedPerPublication(t *testing.T) {
	provider := ai.NewFauxProvider(ai.FauxConfig{})
	provider.SetResponses([]ai.FauxResponseStep{ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{
		ai.FauxText(strings.Repeat("a response streamed in many chunks, ", 12)),
		ai.FauxToolCall("write", map[string]any{"path": "a.txt", "content": strings.Repeat("content streamed as arguments ", 8)}, nil),
	}})})
	share := &messageUpdateShare{}
	streams := []*messageUpdateStream{{}, {}, {}}
	conns := []*Conn{{}, {}, {}}
	var deliveries []sharedDelivery
	var failure error
	fail := func(err error) {
		if failure == nil {
			failure = err
		}
	}
	// deliver prepares and sends the event's frame to extension index, recording it with the whole payload at this call.
	deliver := func(index int, event extension.MessageUpdateEvent) {
		prepared, inStream, err := streams[index].prepareShared(share, conns[index], event)
		if err != nil || !inStream {
			fail(fmt.Errorf("message_update was not prepared as a stream frame: %w", err))
			return
		}
		whole, err := json.Marshal(event)
		if err != nil {
			fail(err)
		}
		streams[index].sent(conns[index], prepared, nil)
		deliveries = append(deliveries, sharedDelivery{extension: index, prepared: prepared, whole: whole})
	}
	shared, interleaved := 0, false
	a, err := agent.NewAgent(agent.AgentOptions{
		Model: &ai.Model{ID: "faux-1", Provider: provider, ProviderMeta: ai.ProviderMetadata{API: "faux", ProviderID: "faux"}},
		DefaultStreamFn: func(ctx context.Context, model *ai.Model, transcript ai.TranscriptContext, options ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
			return model.Provider.Stream(ctx, transcript, options)
		},
		OnEvent: func(ev agent.AgentEvent) {
			update, ok := ev.(agent.MessageUpdateEvent)
			if !ok || failure != nil {
				return
			}
			event := extension.MessageUpdateEvent{Type: "message_update", Message: update.Message, AssistantMessageEvent: update.AssistantMessageEvent}
			if _, shareable := messageUpdateKeyOf(event); !shareable {
				return
			}
			before := share.prepared
			deliver(0, event)
			if _, isDelta := update.AssistantMessageEvent.(ai.TextDeltaEvent); isDelta && !interleaved {
				interleaved = true
				// The first extension's handler awaits its process: the provider runs and publishes before the next extension receives the event.
				_, seq := event.Message.Assistant.Publication()
				_ = agent.EventObservation(ev).AwaitExternal(func() error {
					for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(time.Millisecond) {
						if _, now := event.Message.Assistant.Publication(); now != seq {
							return nil
						}
					}
					fail(errors.New("the provider did not publish while the handler awaited"))
					return nil
				})
				deliver(1, event)
				deliver(2, event)
				if share.prepared != before+2 {
					fail(fmt.Errorf("a publication between deliveries: %d payloads prepared, want 2 (before and after it)", share.prepared-before))
				}
				last := len(deliveries) - 1
				if bytes.Equal(deliveries[last-2].whole, deliveries[last].whole) {
					fail(errors.New("the publication did not change the payload"))
				}
				return
			}
			deliver(1, event)
			deliver(2, event)
			if share.prepared != before+1 {
				fail(fmt.Errorf("three extensions prepared one publication %d times, want 1", share.prepared-before))
			}
			shared++
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Prompt(t.Context(), "go"); err != nil {
		t.Fatal(err)
	}
	if failure != nil {
		t.Fatal(failure)
	}
	if !interleaved || shared < 10 {
		t.Fatalf("the stream must share deltas and publish between deliveries: %d shared, interleaved %v", shared, interleaved)
	}
	held := make([]any, len(streams))
	heldSeq := make([]uint64, len(streams))
	for step, delivery := range deliveries {
		index, prepared := delivery.extension, delivery.prepared
		if prepared.args != nil {
			held[index] = decodeOrdered(t, prepared.args)
		} else {
			if prepared.stream.Base != heldSeq[index] {
				t.Fatalf("delivery %d: patch applies to %d, extension %d holds %d", step, prepared.stream.Base, index, heldSeq[index])
			}
			held[index] = applyOps(t, held[index], prepared.stream.Ops)
		}
		heldSeq[index] = prepared.stream.Seq
		if want := decodeOrdered(t, delivery.whole); !reflect.DeepEqual(held[index], want) {
			t.Fatalf("delivery %d: extension %d holds\n%s\nwant\n%s", step, index, dumpOrdered(held[index]), dumpOrdered(want))
		}
	}
}

// BenchmarkMessageUpdateExtensions prepares the message_update frames of a streamed response for n extensions, with the host's share and without it (each extension preparing its own payload, as before the share). The events are retained live views of a finished stream, so each is prepared at one publication.
func BenchmarkMessageUpdateExtensions(b *testing.B) {
	provider := ai.NewFauxProvider(ai.FauxConfig{})
	provider.SetResponses([]ai.FauxResponseStep{ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{
		ai.FauxText(strings.Repeat("a response streamed in many chunks, ", 40)),
		ai.FauxToolCall("write", map[string]any{"path": "a.txt", "content": strings.Repeat("content streamed as arguments ", 40)}, nil),
	}})})
	var events []extension.MessageUpdateEvent
	a, err := agent.NewAgent(agent.AgentOptions{
		Model: &ai.Model{ID: "faux-1", Provider: provider, ProviderMeta: ai.ProviderMetadata{API: "faux", ProviderID: "faux"}},
		DefaultStreamFn: func(ctx context.Context, model *ai.Model, transcript ai.TranscriptContext, options ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
			return model.Provider.Stream(ctx, transcript, options)
		},
		OnEvent: func(ev agent.AgentEvent) {
			if update, ok := ev.(agent.MessageUpdateEvent); ok {
				events = append(events, extension.MessageUpdateEvent{Type: "message_update", Message: update.Message, AssistantMessageEvent: update.AssistantMessageEvent})
			}
		},
	})
	if err != nil {
		b.Fatal(err)
	}
	if err := a.Prompt(b.Context(), "go"); err != nil {
		b.Fatal(err)
	}
	for _, n := range []int{1, 3, 10} {
		for _, shared := range []bool{false, true} {
			b.Run(fmt.Sprintf("extensions=%d/shared=%v", n, shared), func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					var share *messageUpdateShare
					if shared {
						share = &messageUpdateShare{}
					}
					streams, conns := make([]messageUpdateStream, n), make([]Conn, n)
					for _, event := range events {
						for index := range n {
							prepared, _, err := streams[index].prepareShared(share, &conns[index], event)
							if err != nil {
								b.Fatal(err)
							}
							streams[index].sent(&conns[index], prepared, nil)
						}
					}
				}
				b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*len(events)), "ns/delta")
			})
		}
	}
}
