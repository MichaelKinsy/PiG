package codingagent

import (
	"testing"
)

// Upstream handleResumeSession reports a resume with showStatus("Resumed
// session") and no path (interactive-mode.ts:5592-5606), and the session
// selector's onCancel only closes it (5567-5570).
func TestResumeHandlerMessagesMatchUpstream(t *testing.T) {
	for _, tc := range []struct {
		name   string
		picked bool
		want   []string
	}{
		{"resumed", true, []string{"Resumed session"}},
		{"cancelled", false, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var status, appended []string
			loaded := ""
			sc := &SlashContext{
				Append:     func(s string) { appended = append(appended, s) },
				ShowStatus: func(s string) { status = append(status, s) },
				PickSession: func() (string, bool) {
					return `C:\Users\someone\.pig\agent\sessions\s.jsonl`, tc.picked
				},
				LoadSessionPath: func(path string) error { loaded = path; return nil },
			}
			if err := resumeHandler(sc); err != nil {
				t.Fatal(err)
			}
			if appended != nil || len(status) != len(tc.want) || (len(tc.want) > 0 && status[0] != tc.want[0]) {
				t.Fatalf("status = %q, appended = %q, want status %q and nothing appended", status, appended, tc.want)
			}
			if tc.picked != (loaded != "") {
				t.Fatalf("loaded = %q with picked = %v", loaded, tc.picked)
			}
		})
	}
}
