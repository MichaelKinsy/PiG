//go:build !race

package tui

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testscaling"
)

// Complexity gates for the per-frame and per-keystroke rendering paths. n is the number of transcript messages unless a case says otherwise.

func scalingMainScreen(n int) (*TuiMainScreen, *Container) {
	ui := NewWithOutput(io.Discard, 100, 40)
	chat := buildSessionChat(n)
	ui.Add(chat)
	ui.Render()
	return ui, chat
}

func TestScalingMainScreenFrames(t *testing.T) {
	// Pi's TUI.render (tui.ts doRender) renders every child and compares every line with the previous frame, so its frame is linear in the transcript. PiG's frame keeps the lines before the first child that changed (Container window) and starts the line resets and the differential scan there, so a frame that changes the end of the transcript allocates the same at any length; TestWindowedFramesEqualWholeFrames proves the bytes equal. Each case renders warm frames first: the first frame after a whole render allocates the arrays later frames reuse.
	testscaling.Check(t, testscaling.Case{Name: "main screen streaming delta frame at transcript n", Size: 200, Bound: testscaling.Constant, Prepare: func(n int) func() {
		ui, chat := scalingMainScreen(n)
		tail := NewAssistantMessageComponent(nil, false, nil, "", nil, nil)
		chat.Add(tail)
		text := "token "
		for range 3 {
			tail.SetContent([]AssistantSegment{{Text: text}})
			ui.Render()
			text += "token "
		}
		return func() {
			tail.SetContent([]AssistantSegment{{Text: text}})
			ui.Render()
		}
	}})
	testscaling.Check(t, testscaling.Case{Name: "main screen keystroke frame at transcript n", Size: 200, Bound: testscaling.Constant, Prepare: func(n int) func() {
		ui, _ := scalingMainScreen(n)
		editor := NewEditor()
		ui.Add(editor)
		for range 3 {
			editor.HandleInput("x")
			ui.Render()
		}
		return func() {
			editor.HandleInput("x")
			ui.Render()
		}
	}})
	testscaling.Check(t, testscaling.Case{Name: "main screen message mount frame at transcript n", Size: 200, Bound: testscaling.Constant, Prepare: func(n int) func() {
		ui, chat := scalingMainScreen(n)
		for range 3 {
			chat.Add(NewUserMessageComponent("Warm **up**.", nil, 1, nil))
			ui.Render()
		}
		return func() {
			chat.Add(NewUserMessageComponent("One more **thing**.", nil, 1, nil))
			ui.Render()
		}
	}})
	// A width change re-wraps every message and redraws the whole transcript (tui.ts doRender widthChanged -> fullRender(true)).
	testscaling.Check(t, testscaling.Case{Name: "main screen width change at transcript n", Size: 100, Bound: testscaling.Linear, Prepare: func(n int) func() {
		ui, _ := scalingMainScreen(n)
		return func() {
			ui.SetFixedSize(80, 40)
			ui.Render()
		}
	}})
}

// linearWithFrameCopy is the byte bound of a streamed message: Render returns a new slice of one 16-byte header per row on
// every frame (the component's output must not alias what an earlier frame returned), so bytes grow a little faster than the
// text. The allocation count is gated linear. Allocated bytes of a path that renders the whole text on every frame grow
// with slope 2 and fail this bound.
const linearWithFrameCopy testscaling.Order = 1.2

func scalingAltScreen(n int) (*TuiAltScreen, *Container) {
	ui := NewTuiAltScreenWithOutput(io.Discard, 100, 40, TuiAltScreenOptions{})
	ui.Start()
	chat := buildSessionChat(n)
	ui.Add(chat)
	ui.Render()
	return ui, chat
}

func TestScalingAltScreenFrames(t *testing.T) {
	// Fullscreen (Pi's default) lays the transcript out in a scroll view: RenderLayoutFrame asks the container for its lines each frame (tui-alt-screen.ts doRender renders the whole document). PiG's container keeps the lines before the first child that changed, so a streaming frame allocates the same at any transcript length; the time that still grows is the dirty poll of every child.
	testscaling.Check(t, testscaling.Case{Name: "fullscreen streaming delta frame at transcript n", Size: 150, Bound: testscaling.Constant, Prepare: func(n int) func() {
		ui, chat := scalingAltScreen(n)
		tail := NewAssistantMessageComponent(nil, false, nil, "", nil, nil)
		chat.Add(tail)
		text := "token "
		for range 4 {
			tail.SetContent([]AssistantSegment{{Text: text}})
			ui.Render()
			text += "token "
		}
		return func() {
			tail.SetContent([]AssistantSegment{{Text: text}})
			ui.Render()
		}
	}})
	testscaling.Check(t, testscaling.Case{Name: "fullscreen keystroke frame at transcript n", Size: 150, Bound: testscaling.Constant, Prepare: func(n int) func() {
		ui, chat := scalingAltScreen(n)
		editor := NewEditor()
		chat.Add(editor)
		for range 4 {
			editor.HandleInput("x")
			ui.Render()
		}
		return func() {
			editor.HandleInput("x")
			ui.Render()
		}
	}})
}

func TestScalingStreamingMessage(t *testing.T) {
	// Pi's Markdown component re-lexes and re-wraps the whole text on each change (markdown.ts render), so each streamed delta costs O(text) and a streamed message is quadratic in its length. PiG resumes a render from the last block start the new text cannot have changed (markdown_resume.go), so a streamed message is linear.
	testscaling.Check(t, testscaling.Case{Name: "markdown streaming of n deltas", Size: 100, Bound: testscaling.Linear, BytesBound: linearWithFrameCopy, Prepare: func(n int) func() {
		markdown := NewMarkdown("")
		return func() {
			var text strings.Builder
			for i := range n {
				text.WriteString("word ")
				if i%20 == 19 {
					text.WriteString("\n\n")
				}
				markdown.SetText(text.String())
				markdown.Render(100)
			}
		}
	}})
	// An answer of headings, lists, a table, a quote and a code block, 24 characters per delta: the stream bench-marked at 5k, 20k and 60k characters (the tail of the 60k stream is the same linear path).
	testscaling.Check(t, testscaling.Case{Name: "markdown long answer streaming of n characters", Size: 6000, Bound: testscaling.Linear, BytesBound: linearWithFrameCopy, Prepare: func(n int) func() {
		answer := longAnswer(n)
		markdown := NewMarkdown("")
		return func() {
			for end := 24; ; end += 24 {
				end = min(end, len(answer))
				markdown.SetText(answer[:end])
				markdown.Render(160)
				if end == len(answer) {
					return
				}
			}
		}
	}})
	// The Markdown the interactive mode's assistant block holds: the theme it builds (a copy of GetMarkdownTheme with the code block indent) and a display transform that leaves the text as it is, as the Mermaid transform does for a message with no Mermaid fence.
	testscaling.Check(t, testscaling.Case{Name: "markdown long answer streaming with a theme copy and a transform of n characters", Size: 6000, Bound: testscaling.Linear, BytesBound: linearWithFrameCopy, Prepare: func(n int) func() {
		answer := longAnswer(n)
		theme, indent := GetMarkdownTheme(), "  "
		theme.CodeBlockIndent = &indent
		markdown := NewMarkdownWithOptions("", 0, 0, &theme, nil, &MarkdownOptions{Transform: func(markdown string, width int) string { return markdown }})
		return func() {
			for end := 24; ; end += 24 {
				end = min(end, len(answer))
				markdown.SetText(answer[:end])
				markdown.Render(160)
				if end == len(answer) {
					return
				}
			}
		}
	}})
	testscaling.Check(t, testscaling.Case{Name: "assistant message streaming of n deltas", Size: 100, Bound: testscaling.Linear, BytesBound: linearWithFrameCopy, Prepare: func(n int) func() {
		block := NewAssistantMessageComponent(nil, false, nil, "", nil, nil)
		return func() {
			var text strings.Builder
			for i := range n {
				text.WriteString("word ")
				if i%20 == 19 {
					text.WriteString("\n\n")
				}
				block.SetContent([]AssistantSegment{{Text: text.String()}})
				block.Render(100)
			}
		}
	}})
}

func TestScalingEditorKeystroke(t *testing.T) {
	// Pi's Editor lays out the whole buffer on each render (editor.ts layoutText), so a keystroke is linear in the text; n is the number of 60-character lines in the editor.
	testscaling.Check(t, testscaling.Case{Name: "editor keystroke and render with n lines", Size: 50, Bound: testscaling.Linear, Prepare: func(n int) func() {
		editor := NewEditor()
		editor.SetText(strings.Repeat(strings.Repeat("x", 59)+"\n", n))
		editor.Render(100)
		return func() {
			editor.HandleInput("y")
			editor.Render(100)
		}
	}})
}

func TestScalingAutocompleteKeystroke(t *testing.T) {
	// Pi's slash-command completion fuzzy-filters and sorts every command per keystroke (autocomplete.ts getSuggestions over fuzzyFilter), and a path completion lists the directory it completes in (readdirSync), so both are linear in the candidates. n is the number of commands or directory entries.
	testscaling.Check(t, testscaling.Case{Name: "slash command suggestions with n commands", Size: 100, Bound: testscaling.Linear, Prepare: func(n int) func() {
		commands := make([]SlashCommand, n)
		for i := range commands {
			commands[i] = SlashCommand{Name: fmt.Sprintf("command-%d", i), Description: "does a thing"}
		}
		provider := NewCombinedProvider(commands, t.TempDir(), "")
		lines := []string{"/com"}
		return func() {
			provider.GetSuggestions(context.Background(), lines, 0, len(lines[0]), AutocompleteSuggestionOptions{})
		}
	}})
	testscaling.Check(t, testscaling.Case{Name: "path suggestions in a directory of n entries", Size: 50, Bound: testscaling.Linear, Prepare: func(n int) func() {
		dir := t.TempDir()
		for i := range n {
			if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("file-%d.txt", i)), nil, 0o600); err != nil {
				t.Fatal(err)
			}
		}
		provider := NewCombinedProvider(nil, dir, "")
		lines := []string{"./fi"}
		return func() {
			provider.GetSuggestions(context.Background(), lines, 0, len(lines[0]), AutocompleteSuggestionOptions{})
		}
	}})
}

func TestScalingBashOutputFrame(t *testing.T) {
	// A frame of a running !command block shows the context tail of its output: the last 50KB or 2000 lines.
	testscaling.Check(t, testscaling.Case{Name: "bash block frame at output n", Size: 20000, Bound: testscaling.Constant, Prepare: func(n int) func() {
		block := NewBashExecutionComponent("cmd", nil, false, 1)
		line := strings.Repeat("output ", 12) + "\n"
		for range n {
			block.AppendOutput(line)
		}
		block.Render(100)
		return func() {
			block.AppendOutput(line)
			block.Render(100)
		}
	}})
}

func TestScalingBracketedPaste(t *testing.T) {
	// A bracketed paste that arrives over n reads: each read appends to the paste and looks for the end marker.
	testscaling.Check(t, testscaling.Case{Name: "bracketed paste over n reads", Size: 500, Bound: testscaling.Linear, Prepare: func(n int) func() {
		read := strings.Repeat("pasted text ", 80)
		return func() {
			buffer := NewStdinBuffer(StdinBufferOptions{})
			buffer.ProcessString(bracketedPasteStart)
			for range n {
				buffer.ProcessString(read)
			}
			buffer.ProcessString(bracketedPasteEnd)
		}
	}})
}
