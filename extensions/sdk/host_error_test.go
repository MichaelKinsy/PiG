package sdk

import (
	"testing"
)

// The Node runtime rejects a failed host call with the host's message, and
// with "code: message" only when the host names a code. A spawn error that
// pi.exec rejects with reaches the extension as "spawn EINVAL", not ":
// spawn EINVAL".
func TestHostErrorsKeepTheMessageWithoutACode(t *testing.T) {
	calls := map[string]func(Context) error{
		"exec": func(c Context) error { _, err := c.Exec("tool.cmd", nil); return err },
		"exec with options": func(c Context) error {
			_, err := c.ExecWithOptions("tool.cmd", nil, ExecOptions{Timeout: 1})
			return err
		},
		"sendMessage": func(c Context) error { return c.SendMessage("t", "c", true, SendMessageOptions{}) },
		"sendCustomMessage": func(c Context) error {
			return c.SendCustomMessage(CustomMessage{CustomType: "t", Content: "c"}, SendMessageOptions{})
		},
		"sendUserMessage": func(c Context) error { return c.SendUserMessage("hi", "") },
		"appendEntry":     func(c Context) error { return c.AppendEntry("t", nil) },
		"setSessionName":  func(c Context) error { return c.SetSessionName("n") },
		"complete":        func(c Context) error { _, err := c.Complete(nil, nil, nil); return err },
	}
	for name, call := range calls {
		for _, reply := range []struct {
			info errorInfo
			want string
		}{
			{errorInfo{Message: "spawn EINVAL"}, "spawn EINVAL"},
			{errorInfo{Code: "host_failed", Message: "boom"}, "host_failed: boom"},
		} {
			t.Run(name+" "+reply.want, func(t *testing.T) {
				ext := New("host-error")
				var got error
				ext.Command("probe", "probe", func(ctx Context, _ string) error {
					got = call(ctx)
					return nil
				})
				host, _, done := surfaceHost(t, ext, nil)
				defer surfaceShutdown(t, host, done)
				runSurfaceCommand(t, host, "probe", func(*callMsg) *callResultMsg {
					info := reply.info
					return &callResultMsg{Error: &info}
				})
				if got == nil || got.Error() != reply.want {
					t.Fatalf("error = %v, want %q", got, reply.want)
				}
			})
		}
	}
}
