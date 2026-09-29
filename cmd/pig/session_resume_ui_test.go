//go:build linux

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// resumableSession writes a Session log recorded in cwd with one exchange, so it can be resumed, and returns its path.
func resumableSession(t *testing.T, cwd, sessionDir, id string) string {
	t.Helper()
	manager, err := codingagent.NewSessionManagerWithDir(cwd, sessionDir).Create(id, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.AppendMessage(agent.AgentMessage{User: &agent.UserMessage{Role: agent.RoleUser, Content: ai.UserContentBlocks{ai.TextContent{Text: "from elsewhere"}}, Timestamp: 1}}); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.AppendMessage(agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: agent.RoleAssistant, Content: []ai.AssistantContentBlock{ai.TextContent{Text: "reply"}}, Usage: &ai.Usage{Input: 1, Output: 1}, StopReason: "stop", Timestamp: 2}}); err != nil {
		t.Fatal(err)
	}
	return manager.Path()
}

// waitScreen returns once the terminal output holds needle.
func (p *interactivePig) waitScreen(needle, what string) {
	p.t.Helper()
	deadline := time.Now().Add(testbudget.Wait(p.t))
	for !bytes.Contains(p.output.since(0), []byte(needle)) {
		if time.Now().After(deadline) {
			data, _ := os.ReadFile(p.log)
			p.t.Fatalf("never saw %s (%q); extension log:\n%s\nscreen tail: %q", what, needle, data, printableTail(p.output.since(0), 1500))
		}
		time.Sleep(50 * time.Millisecond)
	}
}

const (
	keyEnter = "\r"
	keyDown  = "\x1b[B"
)

// resume runs /resume, widens the picker to every folder (the Sessions under test belong to other folders) and selects the Session whose first message is "from elsewhere".
func (p *interactivePig) resume() {
	p.t.Helper()
	p.command("/resume")
	p.waitScreen("Resume Session", "the session picker")
	p.send("\t")
	p.waitScreen("from elsewhere", "the other folder's Session")
	p.send(keyEnter)
}

// Pi interactive-mode.ts:5590-5626 (handleResumeSession) passes createProjectTrustContext(cwd) to runtimeHost.switchSession, so entering a project that has trust-requiring resources and no recorded decision prompts to trust it (agent-session-runtime.ts:219, main.ts:751). A replacement that never prompts resolves the project as untrusted without asking.
func TestInteractiveResumeIntoAnUntrustedProjectPromptsForTrust(t *testing.T) {
	if testing.Short() {
		t.Skip("starts the pig binary and extension processes")
	}
	p := startInteractivePig(t)
	destination := filepath.Join(p.home, "destination")
	if err := os.MkdirAll(filepath.Join(destination, ".agents", "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	resumableSession(t, destination, filepath.Join(p.home, "sessions"), "destination-session")
	p.resume()
	p.waitScreen("Trust project folder?", "the trust prompt")
	p.send(keyEnter)
	waitReplaceLog(t, p.log, 3, "the resumed Session")
	p.quit()
}

// Pi handleResumeSession offers the current directory when the stored one is gone. Declining shows "Resume cancelled" and keeps the Session; accepting resumes and shows "Resumed session in current cwd".
func TestInteractiveResumeWithAMissingCWDShowsPisStatuses(t *testing.T) {
	if testing.Short() {
		t.Skip("starts the pig binary and extension processes")
	}
	for _, tc := range []struct {
		name, answer, status string
		resumed              bool
	}{
		{"decline", keyDown + keyEnter, "Resume cancelled", false},
		{"accept", keyEnter, "Resumed session in current cwd", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := startInteractivePig(t)
			gone := filepath.Join(p.home, "gone")
			if err := os.MkdirAll(gone, 0o755); err != nil {
				t.Fatal(err)
			}
			resumableSession(t, gone, filepath.Join(p.home, "sessions"), "gone-session")
			if err := os.RemoveAll(gone); err != nil {
				t.Fatal(err)
			}
			p.resume()
			p.waitScreen("Session cwd not found", "the missing cwd prompt")
			p.send(tc.answer)
			p.waitScreen(tc.status, "the resume status")
			if tc.resumed {
				waitReplaceLog(t, p.log, 3, "the resumed Session")
			} else if lines := replaceLogLines(t, p.log); len(lines) != 1 {
				t.Fatalf("a declined resume replaced the Session: %q", lines)
			}
			p.quit()
		})
	}
}

// Pi interactive-mode.ts:1962 binds ctx.switchSession to handleResumeSession, so an extension's switch prompts for a missing cwd and shows the same statuses as /resume.
func TestInteractiveExtensionSwitchSessionWithAMissingCWDPrompts(t *testing.T) {
	if testing.Short() {
		t.Skip("starts the pig binary and extension processes")
	}
	fixture, err := filepath.Abs(filepath.Join("testdata", "session-switch.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	p := startInteractivePigWith(t, fixture)
	gone := filepath.Join(p.home, "gone")
	if err := os.MkdirAll(gone, 0o755); err != nil {
		t.Fatal(err)
	}
	path := resumableSession(t, gone, filepath.Join(p.home, "sessions"), "gone-session")
	if err := os.RemoveAll(gone); err != nil {
		t.Fatal(err)
	}
	p.command("/switch-to " + path)
	p.waitScreen("Session cwd not found", "the missing cwd prompt")
	p.send(keyEnter)
	p.waitScreen("Resumed session in current cwd", "the resume status")
	if !strings.Contains(strings.Join(replaceLogLines(t, p.log), "\n"), "session_start reason=resume") {
		t.Fatalf("the extension switch did not resume: %q", replaceLogLines(t, p.log))
	}
	p.quit()
}

// printableTail returns the last n printable characters of terminal output, without escape sequences.
func printableTail(data []byte, n int) string {
	var out []byte
	inEscape := false
	for _, b := range data {
		switch {
		case b == 0x1b:
			inEscape = true
		case inEscape:
			if (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || b == '~' {
				inEscape = false
			}
		case b >= 0x20 || b == '\n':
			out = append(out, b)
		}
	}
	return string(out[max(0, len(out)-n):])
}

// ctx.switchSession into a project with trust-requiring resources prompts for trust too, because Pi binds it to handleResumeSession and its projectTrustContextFactory.
func TestInteractiveExtensionSwitchSessionIntoAnUntrustedProjectPromptsForTrust(t *testing.T) {
	if testing.Short() {
		t.Skip("starts the pig binary and extension processes")
	}
	fixture, err := filepath.Abs(filepath.Join("testdata", "session-switch.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	p := startInteractivePigWith(t, fixture)
	destination := filepath.Join(p.home, "destination")
	if err := os.MkdirAll(filepath.Join(destination, ".agents", "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := resumableSession(t, destination, filepath.Join(p.home, "sessions"), "destination-session")
	p.command("/switch-to " + path)
	p.waitScreen("Trust project folder?", "the trust prompt")
	p.send(keyEnter)
	p.waitScreen("Resumed session", "the resume status")
	p.quit()
}
