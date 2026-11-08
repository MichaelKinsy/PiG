package tui

// A launcher action's result as a status line (p2-tui-port.md 3.6b). `denied` includes the visitor declining on the consent screen, which needs
// no notice; `failed` means the launcher showed its own error text (the worker never sees it).
//
// pi: apps/p2/src/tui/grant-notice.ts

import "github.com/MichaelKinsy/PiG/apps/p2/tui/runtime"

// GrantNotice shows result as a status line through ctx: done on success, a warning unless the visitor declined.
func GrantNotice(ctx runtime.Notifier, done string, result runtime.GrantResult) {
	switch {
	case result.OK:
		ctx.Notify(done, runtime.NoticeInfo)
	case result.Error == "failed":
		ctx.Notify("The launcher could not do that (see its message)", runtime.NoticeWarning)
	case result.Error != "denied":
		ctx.Notify("Not done ("+result.Error+")", runtime.NoticeWarning)
	}
}
