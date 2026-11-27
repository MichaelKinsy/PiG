package ai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

// Port of Pi main da7e26cc8bca83c15f04e83c9b468e79d8a35cfe (#12451):
// shared SSE parser behaviors across Anthropic, Codex, and pi-messages.
func TestAnthropicSSEStateDropsEmptyBlockCommentsAndEmptyEvent(t *testing.T) {
	t.Run("drops comment lines of events without data", func(t *testing.T) {
		var state anthropicSSEState
		for _, line := range []string{": ping", "", "data: x", ""} {
			ev, ok := state.decode(line)
			if line == "" && ok {
				if ev.Event != "" || ev.Data != "x" || !reflect.DeepEqual(ev.Raw, []string{"data: x"}) {
					t.Fatalf("event = %#v, want Event=\"\" Data=\"x\" Raw=[\"data: x\"]", ev)
				}
			}
		}
	})

	t.Run("does not carry an empty event name into the next event", func(t *testing.T) {
		var state anthropicSSEState
		var got []serverSentEvent
		for _, line := range []string{"event:", "", "data: x", ""} {
			if ev, ok := state.decode(line); ok {
				got = append(got, ev)
			}
		}
		if len(got) != 1 || got[0].Event != "" || got[0].Data != "x" || !reflect.DeepEqual(got[0].Raw, []string{"data: x"}) {
			t.Fatalf("got = %#v, want single event with empty Event and Raw=[\"data: x\"]", got)
		}
	})
}

func TestCodexSSEFramesHandlesCRLFAndLoneCRAndMultiData(t *testing.T) {
	for _, tc := range []struct {
		name string
		sep  string
	}{
		{"LF", "\n"},
		{"CRLF", "\r\n"},
		{"CR", "\r"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := strings.Join([]string{
				": keepalive",
				`data: {"type":"response.output_text.delta",`,
				`data:  "delta":"hi"}`,
				"",
				`data: {"type":"response.completed"}`,
				"",
			}, tc.sep)
			for chunkSize := 1; chunkSize <= len(raw); chunkSize++ {
				var frames codexSSEFrames
				var dec codexTextDecoder
				var events []string
				for i := 0; i < len(raw); i += chunkSize {
					end := min(i+chunkSize, len(raw))
					frames.add(&dec, []byte(raw[i:end]), false)
					for {
						ev, ok := frames.next()
						if !ok {
							break
						}
						events = append(events, ev)
					}
				}
				frames.add(&dec, nil, true)
				for {
					ev, ok := frames.next()
					if !ok {
						break
					}
					events = append(events, ev)
				}
				want := []string{
					"{\"type\":\"response.output_text.delta\",\n \"delta\":\"hi\"}",
					"{\"type\":\"response.completed\"}",
				}
				if !reflect.DeepEqual(events, want) {
					t.Fatalf("sep=%q chunkSize=%d events=%#v want=%#v", tc.sep, chunkSize, events, want)
				}
			}
		})
	}
}

func TestPiMessagesSSEHandlesMultiDataAndLoneCRAndNullEvent(t *testing.T) {
	for _, tc := range []struct {
		name string
		sep  string
	}{
		{"LF", "\n"},
		{"CRLF", "\r\n"},
		{"CR", "\r"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stream := strings.Join([]string{
				`data: {"type":"start"}`,
				"",
				`data: null`,
				"",
				`data: {"type":"done",`,
				`data:  "reason":"stop","responseId":"resp_1"}`,
				"",
			}, tc.sep)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, stream)
			}))
			defer server.Close()
			provider := NewPiMessagesProvider(PiMessagesConfig{
				APIKey:  "test-key",
				Model:   "test-model",
				BaseURL: server.URL,
			})
			var received []map[string]any
			s, err := provider.Stream(t.Context(), NormalizeContext(Context{
				Messages: []Message{UserMessage{Content: UserText("hi"), Timestamp: 1}},
			}), StreamOptions{
				OnProviderStreamEvent: func(_ context.Context, data any, _ *Model) error {
					raw, err := json.Marshal(data)
					if err != nil {
						return err
					}
					var m map[string]any
					if err := json.Unmarshal(raw, &m); err != nil {
						return err
					}
					received = append(received, m)
					return nil
				},
			})
			if err != nil {
				t.Fatalf("Stream failed: %v", err)
			}
			msg := s.Result()
			if msg.StopReason != StopReasonStop || msg.ResponseID != "resp_1" {
				t.Fatalf("sep=%q msg = stop=%q resp=%q err=%q", tc.sep, msg.StopReason, msg.ResponseID, msg.ErrorMessage)
			}
			if len(received) != 2 || received[0]["type"] != "start" || received[1]["type"] != "done" {
				t.Fatalf("sep=%q received = %#v", tc.sep, received)
			}
		})
	}
}
