//go:build parity

package runner

import "testing"

func TestLatestRPCRequestIDWithTitlePicksTheNamedDialog(t *testing.T) {
	output := `{"type":"extension_ui_request","id":"id-first","method":"select","title":"First?"}
{"type":"extension_ui_request","id":"id-second","method":"select","title":"Second?"}
{"type":"extension_ui_request","id":"id-first-again","method":"select","title":"First?"}
`
	for title, want := range map[string]string{"First?": "id-first-again", "Second?": "id-second"} {
		got, err := latestRPCRequestIDWithTitle(output, title)
		if err != nil || got != want {
			t.Fatalf("latestRPCRequestIDWithTitle(%q) = %q, %v; want %q", title, got, err, want)
		}
	}
	if _, err := latestRPCRequestIDWithTitle(output, "Third?"); err == nil {
		t.Fatal("a title no dialog has resolved an id")
	}
}
