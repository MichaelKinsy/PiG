package cli

// pi: packages/coding-agent/src/modes/json-event.ts

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

type printModeJSONMarshaler func() ([]byte, error)

func (marshal printModeJSONMarshaler) MarshalJSON() ([]byte, error) { return marshal() }

// Pi agent-session.ts:915-936 notifies public subscribers before persisting message_end. print-mode.ts:108-111 converts and JSON.stringify-serializes inside that notification, not in a later output reader.
func TestJSONModeObservesAndSerializesBeforePersistence(t *testing.T) {
	provider := ai.NewFauxProvider(ai.FauxConfig{})
	provider.SetResponses(fauxSteps(fauxTextResponse("observed-once")))
	host := printModeTestHost(t, provider)
	converted, serialized := 0, 0
	checkBeforePersistence := func(at string) error {
		persisted, err := printModeHasAssistantEntry(host.Session.SessionDir)
		if err != nil {
			return err
		}
		if persisted {
			t.Errorf("%s ran after message_end persistence", at)
		}
		return nil
	}
	result := runPrintModeForTest(t, host, printModeOptions{
		Mode: "json", InitialMessage: "probe",
		convertEvent: func(event agent.AgentEvent) ([]any, error) {
			end, ok := event.(agent.MessageEndEvent)
			if !ok || end.Message.Assistant == nil {
				return rpcAgentEvent(event)
			}
			converted++
			if err := checkBeforePersistence("conversion"); err != nil {
				return nil, err
			}
			return []any{printModeJSONMarshaler(func() ([]byte, error) {
				serialized++
				if err := checkBeforePersistence("serialization"); err != nil {
					return nil, err
				}
				return []byte(`{"type":"observation_probe"}`), nil
			})}, nil
		},
	})
	if result.err != nil || result.stderr != "" {
		t.Fatalf("print result: %v, stderr=%q", result.err, result.stderr)
	}
	if converted != 1 || serialized != 1 || strings.Count(result.stdout, `"type":"observation_probe"`) != 1 {
		t.Fatalf("one assistant end: converted=%d serialized=%d stdout=%s", converted, serialized, result.stdout)
	}
	persisted, err := printModeHasAssistantEntry(host.Session.SessionDir)
	if err != nil || !persisted {
		t.Fatalf("final assistant was not persisted: %t, %v", persisted, err)
	}
}

func TestJSONModeSerializationFailureFailsTheRun(t *testing.T) {
	provider := ai.NewFauxProvider(ai.FauxConfig{})
	provider.SetResponses(fauxSteps(fauxTextResponse("answer")))
	failedSerializations := 0
	result := runPrintModeForTest(t, printModeTestHost(t, provider), printModeOptions{
		Mode: "json", InitialMessage: "probe",
		convertEvent: func(event agent.AgentEvent) ([]any, error) {
			if end, ok := event.(agent.MessageEndEvent); ok && end.Message.Assistant != nil {
				return []any{
					map[string]string{"type": "before_serialization_failure"},
					printModeJSONMarshaler(func() ([]byte, error) { failedSerializations++; return nil, errors.New("serializer failed") }),
					printModeJSONMarshaler(func() ([]byte, error) {
						t.Error("serialized an event after the first serialization failure")
						return []byte(`{"type":"after_serialization_failure"}`), nil
					}),
				}, nil
			}
			return rpcAgentEvent(event)
		},
	})
	if !errors.Is(result.err, errPrintModeHandled) || !strings.Contains(result.stderr, "serializer failed") {
		t.Fatalf("serialization failure was swallowed: err=%v stderr=%q", result.err, result.stderr)
	}
	if failedSerializations != 1 || strings.Count(result.stdout, `"type":"before_serialization_failure"`) != 1 || strings.Contains(result.stdout, `"type":"after_serialization_failure"`) {
		t.Fatalf("partial output or first-error ownership changed: failures=%d stdout=%s", failedSerializations, result.stdout)
	}
}

func BenchmarkJSONModeObservation(b *testing.B) {
	for _, size := range []int{0, 1024, 65536} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			text := strings.Repeat("x", size)
			host := printModeTestHost(b, ai.NewFauxProvider(ai.FauxConfig{}))
			host.Session.NoSession = true
			b.ReportAllocs()
			for b.Loop() {
				provider := ai.NewFauxProvider(ai.FauxConfig{TokenSize: &ai.FauxTokenSize{Min: new(128), Max: new(128)}})
				provider.SetResponses(fauxSteps(fauxTextResponse(text)))
				model := *host.Session.Model
				model.Provider = provider
				host.Session.Model = &model
				if err := startPrintModeErr(b.Context(), host, printModeOptions{Mode: "json", InitialMessage: "probe", stdout: io.Discard, stderr: io.Discard}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func printModeHasAssistantEntry(dir string) (bool, error) {
	files, err := os.ReadDir(dir)
	if err != nil {
		return false, err
	}
	for _, file := range files {
		if file.IsDir() || filepath.Ext(file.Name()) != ".jsonl" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, file.Name()))
		if err != nil {
			return false, err
		}
		decoder := json.NewDecoder(bytes.NewReader(data))
		for {
			var entry struct {
				Type    string
				Message struct{ Role string }
			}
			if err := decoder.Decode(&entry); err != nil {
				if errors.Is(err, io.EOF) {
					break
				}
				return false, fmt.Errorf("decode persisted print event: %w", err)
			}
			if entry.Type == "message" && entry.Message.Role == agent.RoleAssistant {
				return true, nil
			}
		}
	}
	return false, nil
}
