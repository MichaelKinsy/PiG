package codingagent

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// Pi's common auth notification path (interactive-mode.ts:6302-6313 notifyAuthDialog) renders a waiting line after a device code only, shows an info event with its links, and opens only auth URLs (login-dialog.ts:97-111 showAuth).
func TestProviderOwnedAuthDialogNotifications(t *testing.T) {
	for _, tc := range []struct {
		name     string
		event    ai.AuthEvent
		text     []string
		absent   []string
		launches []string
	}{
		{"device", ai.AuthDeviceCodeEvent{VerificationURI: deviceProbeURI, UserCode: "WDJB-MJHT"}, []string{deviceProbeURI, "Enter code: WDJB-MJHT", "Waiting for authentication..."}, nil, nil},
		{"url", ai.AuthURLEvent{URL: authProbeURL}, []string{authProbeURL}, []string{"Waiting for authentication..."}, []string{authProbeURL}},
		{"info", ai.AuthInfoEvent{Message: "Use your work account", Links: []ai.AuthInfoLink{{URL: "https://example.test/help", Label: "Help"}, {URL: "https://example.test/terms"}}}, []string{"Use your work account", "Help: https://example.test/help", "https://example.test/terms"}, []string{"Waiting for authentication..."}, nil},
		{"progress", ai.AuthProgressEvent{Message: "Checking"}, []string{"Checking"}, []string{"Waiting for authentication..."}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original := openBrowser
			t.Cleanup(func() { openBrowser = original })
			var launches []string
			openBrowser = func(url string) error { launches = append(launches, url); return nil }
			m := newPostLoginTestMode(t)
			ctx, cancel := context.WithCancelCause(t.Context())
			m.runCtx = ctx
			dialog := m.newLoginDialog("Auth probe", func() { cancel(errLoginAborted) })
			notifications := make(chan ai.AuthEvent)
			done := make(chan error, 1)
			finished := make(chan error, 1)
			go func() { finished <- m.runAuthDialog(ctx, cancel, dialog, nil, notifications, done) }()
			defer func() {
				done <- nil
				if err := <-finished; err != nil {
					t.Error(err)
				}
				cancel(nil)
			}()
			notifications <- tc.event
			observed := make(chan string, 1)
			m.uiTaskCh <- func() { observed <- plainRender(dialog) }
			text := <-observed
			for _, want := range tc.text {
				if !strings.Contains(text, want) {
					t.Errorf("auth dialog missing %q: %q", want, text)
				}
			}
			for _, unwanted := range tc.absent {
				if strings.Contains(text, unwanted) {
					t.Errorf("auth dialog shows %q, which Pi does not: %q", unwanted, text)
				}
			}
			if !slices.Equal(launches, tc.launches) {
				t.Errorf("browser launches=%q, want %q", launches, tc.launches)
			}
		})
	}
}
