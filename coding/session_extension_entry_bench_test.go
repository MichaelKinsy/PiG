package coding

import (
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

type appendEntryCapture struct {
	appendEntry func(customType string, data any, direct *subprocess.DirectEntryAppend) error
}

func (c *appendEntryCapture) SetHostAction(key string, fn any) {
	if key == "appendEntry" {
		c.appendEntry = fn.(func(string, any, *subprocess.DirectEntryAppend) error)
	}
}

// BenchmarkExtensionAppendEntry is the host cost of one pi.appendEntry call of an extension a Session's resource loader loaded, composed as bindLoaderExtensionHost composes it: "log-only" is the call that only writes the log (what the Session did before it reported the entry, and what ctx.sessionManager.appendCustomEntry still does), "event" is the call that also delivers entry_appended to a listener, which each op waits for.
func BenchmarkExtensionAppendEntry(b *testing.B) {
	b.Setenv("PIG_HOME", b.TempDir())
	services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: b.TempDir()})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(services.Close)
	session, err := NewSession(services, SessionOptions{Model: &ai.Model{ID: "primary", Provider: ai.NewFauxProvider(ai.FauxConfig{})}, NoSession: true, SkipBuiltinTools: true})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = session.Close() })
	// The listener below is the only sink, as it is for a child session: nothing reads Events.
	session.DiscardEvents()
	current := func() (*icodingagent.Session, error) { return session.Inner(), nil }
	delivered := make(chan struct{}, 1)
	session.Subscribe(func(event agent.AgentEvent) {
		if _, ok := event.(agent.EntryAppendedEvent); ok {
			delivered <- struct{}{}
		}
	})
	data := map[string]any{"n": 1}
	b.Run("log-only", func(b *testing.B) {
		var capture appendEntryCapture
		icodingagent.BindAppendEntryAction(&capture, current, nil)
		b.ReportAllocs()
		for b.Loop() {
			if err := capture.appendEntry("bench", data, nil); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("event", func(b *testing.B) {
		var capture appendEntryCapture
		icodingagent.BindAppendEntryAction(&capture, current, session.reportExtensionEntry)
		b.ReportAllocs()
		for b.Loop() {
			if err := capture.appendEntry("bench", data, nil); err != nil {
				b.Fatal(err)
			}
			<-delivered
		}
	})
}
