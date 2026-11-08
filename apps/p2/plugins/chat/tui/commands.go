package tui

// Chat's TUI commands (p2-tui-port.md section 6.3), ported from the p2.* actions to pi's ids. They reach the live surface through `ctx.Surface("chat")`, so a
// facet reload never leaves a stale handler.
//
// pi: apps/p2/plugins/chat/tui-commands.ts

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/runtime"
)

func chat(ctx runtime.TuiContext) *ChatSurface {
	surface, _ := ctx.Surface("chat").(*ChatSurface)
	return surface
}

// Commands are chat's commands in registration order.
func Commands() p2runtime.Keyed[runtime.TuiCommand] {
	run := func(do func(chat *ChatSurface, args string)) func(ctx runtime.TuiContext, args string) error {
		return func(ctx runtime.TuiContext, args string) error {
			if surface := chat(ctx); surface != nil {
				do(surface, args)
			}
			return nil
		}
	}
	return p2runtime.Keyed[runtime.TuiCommand]{
		p2runtime.Entry("app.interrupt", runtime.TuiCommand{
			Title: "Stop the agent", Category: "Chat",
			// pi: Escape closes the editor's autocomplete first, and aborts a running compaction.
			When: "chat && participant && !autocomplete && !overlay",
			Run:  run(func(chat *ChatSurface, _ string) { chat.Interrupt() }),
		}),
		p2runtime.Entry("app.clear", runtime.TuiCommand{
			Title: "Clear editor", Category: "Chat",
			// pi: Ctrl+C clears the editor; a second press within 500 ms exits (ChatSurface.Clear).
			When: "chat && editorFocused && !autocomplete && !overlay",
			Run:  run(func(chat *ChatSurface, _ string) { chat.Clear() }),
		}),
		p2runtime.Entry("app.message.followUp", runtime.TuiCommand{
			Title: "Queue follow-up message", Category: "Chat", When: "chat && participant && !overlay",
			Run: run(func(chat *ChatSurface, _ string) { chat.Send("followUp") }),
		}),
		p2runtime.Entry("app.message.dequeue", runtime.TuiCommand{
			Title: "Restore your queued messages", Category: "Chat", When: "chat && participant && !overlay",
			Run: run(func(chat *ChatSurface, _ string) { chat.DequeueOwn() }),
		}),
		p2runtime.Entry("p2.chat.compact", runtime.TuiCommand{
			Title: "Compact the context", Category: "Session", When: "chat && participant && !overlay",
			Slash: &runtime.TuiCommandSlash{Name: "compact", ArgumentHint: "[instructions]"},
			Run:   run(func(chat *ChatSurface, args string) { chat.Compact(args) }),
		}),
		// pi's /tree: the session's branches (docs/tree.md).
		p2runtime.Entry("app.session.tree", runtime.TuiCommand{
			Title: "Continue the session from an earlier message", Category: "Session", When: "chat && owner && !overlay",
			Slash: &runtime.TuiCommandSlash{Name: "tree"},
			Run:   run(func(chat *ChatSurface, _ string) { chat.OpenTree() }),
		}),
		// pi's /fork and /clone as new sessions (Sessions.fork/clone; the server copies the store).
		p2runtime.Entry("app.session.fork", runtime.TuiCommand{
			Title: "Fork from a message into a new session", Category: "Session", When: "chat && owner && !overlay",
			Slash: &runtime.TuiCommandSlash{Name: "fork"},
			Run:   run(func(chat *ChatSurface, _ string) { chat.OpenFork() }),
		}),
		p2runtime.Entry("p2.session.clone", runtime.TuiCommand{
			Title: "Clone the session", Category: "Session", When: "chat && owner && !overlay",
			Slash: &runtime.TuiCommandSlash{Name: "clone"},
			Run:   run(func(chat *ChatSurface, _ string) { chat.CloneSession() }),
		}),
		p2runtime.Entry("p2.chat.newContext", runtime.TuiCommand{
			Title: "Start a new context", Category: "Session", When: "chat && participant && owner && !overlay",
			Slash: &runtime.TuiCommandSlash{Name: "new-context", ArgumentHint: "[handoff]"},
			Run:   run(func(chat *ChatSurface, args string) { chat.NewContext(args) }),
		}),
		// P9: local files reach the server only through the launcher (its picker or a confirmation of the path).
		p2runtime.Entry("p2.chat.attach", runtime.TuiCommand{
			Title: "Attach a local file", Category: "Chat", When: "chat && participant && !overlay",
			Slash: &runtime.TuiCommandSlash{Name: "attach", ArgumentHint: "[path]"},
			Run: func(ctx runtime.TuiContext, args string) error {
				payload := map[string]any{}
				if args != "" {
					payload["path"] = args
				}
				// The launcher's consent screen blocks: off the owner loop.
				go func() {
					result := ctx.Grant("attach", payload)
					ctx.Post(func() {
						file, ok := parseUpload(result.Value)
						switch {
						case result.OK && ok:
							if surface := chat(ctx); surface != nil {
								surface.AddAttachment(file)
							}
						case !result.OK && result.Error != "denied":
							ctx.Notify("Not attached ("+result.Error+")", runtime.NoticeWarning)
						}
					})
				}()
				return nil
			},
		}),
		p2runtime.Entry("p2.chat.detach", runtime.TuiCommand{
			Title: "Remove the last attachment", Category: "Chat", Keys: []string{"backspace"},
			When: "chat && participant && editorEmpty && attachments && !overlay",
			Run:  run(func(chat *ChatSurface, _ string) { chat.Detach(false) }),
		}),
		p2runtime.Entry("p2.chat.detachAll", runtime.TuiCommand{
			Title: "Remove all attachments", Category: "Chat", When: "chat && participant && attachments && !overlay",
			Slash: &runtime.TuiCommandSlash{Name: "detach"},
			Run:   run(func(chat *ChatSurface, _ string) { chat.Detach(true) }),
		}),
		// pi's /copy (P5): the launcher shows the full text and copies only after a local confirmation (C grant).
		p2runtime.Entry("p2.chat.copy", runtime.TuiCommand{
			Title: "Copy the last answer", Category: "Chat", When: "chat && !overlay",
			Slash: &runtime.TuiCommandSlash{Name: "copy"},
			Run: func(ctx runtime.TuiContext, _ string) error {
				var text string
				if surface := chat(ctx); surface != nil {
					text = surface.LastAssistantText()
				}
				if text == "" {
					ctx.Notify("Nothing to copy yet", runtime.NoticeWarning)
					return nil
				}
				go func() {
					result := ctx.Grant("copy", map[string]any{"text": utf16Prefix(text, 1024*1024), "label": "the last answer"})
					if !result.OK && result.Error != "denied" {
						ctx.Post(func() { ctx.Notify("Not copied ("+result.Error+")", runtime.NoticeWarning) })
					}
				}()
				return nil
			},
		}),
	}
}

// utf16Prefix is text.slice(0, units): at most units UTF-16 code units, never cutting a character.
func utf16Prefix(text string, units int) string {
	count := 0
	for i, r := range text {
		width := 1
		if r > 0xffff {
			width = 2
		}
		if count+width > units {
			return text[:i]
		}
		count += width
	}
	return text
}

// LauncherUpload is an upload the launcher made (P9): the server's upload ID and what the chip shows. Never a local path.
type LauncherUpload struct {
	ID       string
	Name     string
	Size     float64
	MimeType string
}

var uploadID = regexp.MustCompile(`(?i)^[0-9a-f-]{36}$`)

// parseUpload reads a launcher's upload description (any JSON-shaped value), false when it is not one.
func parseUpload(value any) (LauncherUpload, bool) {
	if value == nil {
		return LauncherUpload{}, false
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return LauncherUpload{}, false
	}
	var fields struct {
		ID       *string  `json:"id"`
		Name     *string  `json:"name"`
		Size     *float64 `json:"size"`
		MimeType *string  `json:"mimeType"`
	}
	if json.Unmarshal(encoded, &fields) != nil || fields.ID == nil || !uploadID.MatchString(*fields.ID) || fields.Name == nil || fields.Size == nil {
		return LauncherUpload{}, false
	}
	upload := LauncherUpload{ID: *fields.ID, Name: truncateUnits(*fields.Name, 255), Size: *fields.Size}
	if fields.MimeType != nil {
		upload.MimeType = truncateUnits(*fields.MimeType, 100)
	}
	return upload, true
}

func truncateUnits(text string, units int) string {
	if utf8.RuneCountInString(text) <= units {
		return text
	}
	return utf16Prefix(text, units)
}

// FormatSize is a file size as the chips show it.
func FormatSize(bytes float64) string {
	switch {
	case bytes < 1024:
		return fmt.Sprintf("%g B", bytes)
	case bytes < 1048576:
		return fmt.Sprintf("%d KB", jsRound(bytes/1024))
	}
	return strings.TrimSpace(fmt.Sprintf("%.1f MB", bytes/1048576))
}

// jsRound is Math.round.
func jsRound(value float64) int {
	return int(value + 0.5)
}

// answerOwner is which surface registered each launcher answer of a runtime (the loop's own state: a reload disposes the old surface before the new one is made,
// but a late dispose never takes the new one's answer away).
var answerOwner = map[answerKey]*ChatSurface{}

type answerKey struct {
	rt   *runtime.TuiRuntime
	kind string
}

// registerLauncherAnswers answers the launcher's grant-bound questions (section 3.1 rule 4, `externalEditor`): the text Ctrl+X copies, and the editor text for
// Ctrl+G and its result. Only the mounted chat surface answers; the launcher drops results of a grant that ended (navigation, swap). It returns the release.
func (s *ChatSurface) registerLauncherAnswers() func() {
	rt := s.runtime
	own := map[string]func(payload any) any{
		"copyText": func(any) any { return map[string]any{"text": s.LastAssistantText()} },
		// Ctrl+V and p2.attach: the launcher uploaded a local file after its own confirmation.
		"attached": func(payload any) any {
			added := false
			if file, ok := parseUpload(payload); ok {
				added = s.AddAttachment(file)
			}
			return map[string]any{"added": added}
		},
		"editorText": func(any) any {
			if !s.Editable() {
				return nil
			}
			return map[string]any{"text": s.Editor.GetExpandedText()}
		},
		"editorResult": func(payload any) any {
			object, _ := payload.(map[string]any)
			text, ok := object["text"].(string)
			if !ok || !s.Editable() {
				return map[string]any{"applied": false}
			}
			s.Editor.SetText(text)
			rt.Changed()
			return map[string]any{"applied": true}
		},
	}
	for kind, answer := range own {
		rt.LauncherAnswers[kind] = answer
		answerOwner[answerKey{rt, kind}] = s
	}
	return func() {
		for kind := range own {
			if key := (answerKey{rt, kind}); answerOwner[key] == s {
				delete(answerOwner, key)
				delete(rt.LauncherAnswers, kind)
			}
		}
	}
}
