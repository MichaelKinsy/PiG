package tui

import (
	"encoding/json"
	"testing"
)

// Mirrors upstream renderers/{grep,find,ls}.ts formatGrepCall, formatFindCall,
// and formatLsCall byte for byte: str() invalid-arg markers, $HOME shortening
// of the search path, and `limit !== undefined` suffixes.
func TestListToolHeaders(t *testing.T) {
	th := ActiveTheme()
	title := func(s string) string { return th.ToolTitle + "\x1b[1m" + s + SGRBoldDimReset + SGRFgReset }
	c := func(color, s string) string { return color + s + SGRFgReset }
	invalid := c(th.Error, "[invalid arg]")
	t.Setenv("HOME", "/home/tester")
	t.Setenv("USERPROFILE", "/home/tester")
	cwd := t.TempDir()

	cases := []struct {
		name string
		got  string
		want string
	}{
		{"grep full", FormatGrepHeader(json.RawMessage(`{"pattern":"TODO","path":"/home/tester/src","glob":"*.go","limit":5}`)),
			title("grep") + " " + c(th.Accent, "/TODO/") + c(th.ToolOutput, " in ~/src") + c(th.ToolOutput, " (*.go)") + c(th.ToolOutput, " limit 5")},
		{"grep defaults", FormatGrepHeader(json.RawMessage(`{"pattern":"x"}`)),
			title("grep") + " " + c(th.Accent, "/x/") + c(th.ToolOutput, " in .")},
		{"grep invalid", FormatGrepHeader(json.RawMessage(`{"pattern":1,"path":2,"glob":3,"limit":null}`)),
			title("grep") + " " + invalid + c(th.ToolOutput, " in "+invalid) + c(th.ToolOutput, " limit null")},
		{"find", FormatFindHeader(json.RawMessage(`{"pattern":"*.go","path":"src","limit":10}`)),
			title("find") + " " + c(th.Accent, "*.go") + c(th.ToolOutput, " in src") + c(th.ToolOutput, " (limit 10)")},
		{"find invalid pattern", FormatFindHeader(json.RawMessage(`{"pattern":[1]}`)),
			title("find") + " " + invalid + c(th.ToolOutput, " in .")},
		{"ls default", FormatLsHeader(json.RawMessage(`{}`), cwd), title("ls") + " " + linkPath(c(th.Accent, "."), ".", cwd)},
		{"ls invalid", FormatLsHeader(json.RawMessage(`{"path":false,"limit":2.5}`), ""),
			title("ls") + " " + invalid + c(th.ToolOutput, " (limit 2.5)")},
	}
	for _, tc := range cases {
		if tc.got != tc.want {
			t.Errorf("%s:\n got %q\nwant %q", tc.name, tc.got, tc.want)
		}
	}
}
