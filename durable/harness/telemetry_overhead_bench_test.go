package harness

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/chord/sqlite/node"
	"github.com/MichaelKinsy/PiG/chord/telemetrysqlite"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/storage"
	"github.com/MichaelKinsy/PiG/telemetry"
)

// benchGeneration drives the Durable harness generation loop: one input submission, one faux model request that streams answer, one
// committed assistant entry, per iteration, over memory storage. sink is the harness telemetry context (nil: off).
func benchGeneration(b *testing.B, sink telemetry.TelemetryContext, answer string) {
	faux := ai.NewFauxProvider(ai.FauxConfig{})
	models := ai.CreateModels()
	models.SetProvider(faux.Provider())
	harness, err := OpenHarness(testContext, storage.NewMemoryStorage(), HarnessOptions{
		Models: models, Registry: CreateRegistry(), Telemetry: sink,
		Settings: func() *HarnessSettings { return &HarnessSettings{} },
		Now:      func() float64 { return 1 },
		OnReport: func(error, durable.ReportInfo) {},
	})
	if err != nil {
		b.Fatal(err)
	}
	root, err := harness.Root(testContext, &RootOptions{Agent: &AgentChange{Model: SetTo(durable.ModelRef{Provider: "faux", ModelId: "faux-1"})}})
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		faux.SetResponses([]ai.FauxResponseStep{fauxAnswer(answer)})
		submitted, err := root.Submit(testContext, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("hi")})
		if err != nil {
			b.Fatal(err)
		}
		if _, err := submitted.Wait(testContext); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	if err := harness.WaitForIdle(testContext); err != nil {
		b.Fatal(err)
	}
}

func BenchmarkDurableGeneration(b *testing.B) {
	for _, size := range []struct {
		name   string
		answer string
	}{{"short", "ok"}, {"long", strings.Repeat("lorem ipsum dolor sit amet ", 200)}} {
		b.Run(size.name+"/off", func(b *testing.B) { benchGeneration(b, nil, size.answer) })
		b.Run(size.name+"/memory", func(b *testing.B) { benchGeneration(b, telemetry.NewInMemoryTelemetryContext(), size.answer) })
		b.Run(size.name+"/sqlite", func(b *testing.B) {
			db, err := node.OpenNodeSqliteDatabase(filepath.Join(b.TempDir(), "telemetry.db"), node.NodeSqliteDatabaseOptions{})
			if err != nil {
				b.Fatal(err)
			}
			sink, err := telemetrysqlite.CreateSqliteTelemetry(db, telemetrysqlite.SqliteTelemetryOptions{})
			if err != nil {
				b.Fatal(err)
			}
			benchGeneration(b, sink, size.answer)
			if err := sink.Close(); err != nil {
				b.Fatal(err)
			}
			_ = db.Close()
		})
	}
}
