package codingagent

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
	"github.com/MichaelKinsy/PiG/tui"
)

func BenchmarkManagedToolStartup(b *testing.B) {
	b.Setenv("PATH", b.TempDir())
	b.Setenv("PIG_OFFLINE", "1")
	tm := tools.NewToolsManager(b.TempDir())
	b.ReportAllocs()
	for b.Loop() {
		m := &InteractiveMode{chatContainer: tui.NewContainer()}
		m.ensureManagedTools(context.Background(), tm)
		_ = m.chatContainer.Render(120)
	}
}
