package coding

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

func BenchmarkSessionPromptOptionsCommand(b *testing.B) {
	services, err := NewServices(ServicesOptions{CWD: b.TempDir(), AgentDir: b.TempDir()})
	if err != nil {
		b.Fatal(err)
	}
	runner := inproc.NewRunner([]extension.Extension{{Commands: map[string]extension.RegisteredCommand{"options": {Name: "options", Handler: func(ctx context.Context, _ string) error {
		_, err := extension.CommandContextFromContext(ctx).GetSystemPromptOptions()
		return err
	}}}}}, services.CWD())
	session, err := NewSession(services, SessionOptions{Model: fakeModel(), NoSession: true, Runner: runner})
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = session.Close() }()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range session.Events() {
		}
	}()
	defer func() { _ = session.Close(); <-done }()
	b.ReportAllocs()
	for b.Loop() {
		if _, err := session.Prompt(context.Background(), "/options"); err != nil {
			b.Fatal(err)
		}
	}
}
