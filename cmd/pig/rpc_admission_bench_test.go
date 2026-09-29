package main

import (
	"io"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
)

func BenchmarkRPCPromptAdmission(b *testing.B) {
	b.Setenv("PIG_HOME", b.TempDir())
	services, err := coding.NewServices(coding.ServicesOptions{CWD: b.TempDir()})
	if err != nil {
		b.Fatal(err)
	}
	model := &ai.Model{ID: "faux-1", Provider: &ai.TestFauxProvider{}, Capabilities: ai.ModelCapabilities{ContextWindow: 128000}}
	b.ReportAllocs()
	for b.Loop() {
		session, err := coding.NewSession(services, coding.SessionOptions{Model: model, NoSession: true, SkipBuiltinTools: true, SystemPrompt: "Admission benchmark."})
		if err != nil {
			b.Fatal(err)
		}
		write := func(v any) { writeJSONLine(io.Discard, v) }
		done := make(chan struct{})
		go func() {
			defer close(done)
			for event := range session.Events() {
				if coding.AcknowledgeEvent(event) {
					continue
				}
				records, err := rpcAgentEvent(event)
				if err != nil {
					panic(err)
				}
				for _, record := range records {
					write(record)
				}
			}
		}()
		turn := &rpcResponseTurn{write: write}
		session.SetRetryContinuationScheduler(turn.after)
		var runs sync.WaitGroup
		admission := &rpcAdmission{ctx: b.Context(), turn: turn, session: session, write: write, runs: &runs, validateModel: func() error { return nil }}
		turn.begin()
		admission.prompt(rpcStringID("bench"), RPCPromptCommand{Message: "What is 20+22?"})
		turn.end()
		runs.Wait()
		if err := session.FlushEvents(b.Context()); err != nil {
			b.Fatal(err)
		}
		admission.tasks.CloseAndWait()
		if err := session.Close(); err != nil {
			b.Fatal(err)
		}
		<-done
	}
}
