package codingagent

import (
	"io"
	"strings"

	"github.com/MichaelKinsy/PiG/tui"
)

// ReplaceSession drives the interactive built-in callbacks on the owner loop.
func (h *TestHarness) ReplaceSession(operation, target string) error {
	reader, writer := io.Pipe()
	h.m.startTerminalInput(h.ctx, reader)
	defer func() { _ = writer.Close(); _ = h.m.stopTerminalInput(); h.m.backgroundTasks.Wait() }()
	var err error
	h.Do(func() {
		sc := h.m.buildSlashContext(h.ctx)
		switch operation {
		case "new":
			err = sc.NewSession()
		case "resume":
			err = sc.LoadSessionPath(target)
		case "fork":
			err = sc.ForkToNewSession(target)
		case "clone":
			_, err = sc.CloneCurrent()
		}
	})
	return err
}

// SeedReplacementTranscript adds a non-status marker that only transcript rebuilding removes.
func (h *TestHarness) SeedReplacementTranscript() {
	h.Do(func() { h.m.chatContainer.Add(tui.NewText("outgoing transient notification")) })
}

// LoadedResources returns the resource listing on the owner loop, rendered 120 columns wide.
func (h *TestHarness) LoadedResources() string {
	return h.LoadedResourcesAt(120)
}

// LoadedResourcesAt returns the resource listing on the owner loop, rendered width columns wide.
func (h *TestHarness) LoadedResourcesAt(width int) string {
	var text string
	h.Do(func() { text = strings.Join(h.m.loadedResourcesContainer.Render(width), "\n") })
	return text
}
