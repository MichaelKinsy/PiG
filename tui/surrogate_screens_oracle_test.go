package tui

// Pi measures a row as a UTF-16 string, where a lone surrogate is nonprinting, and Node encodes it (U+FFFD) when the terminal write runs. PiG's alt and
// main screens are compared with Pi's, byte for byte per write after that encoding, over rows holding lone surrogates at the width edge, in escape
// sequences and next to wide text, and over a frame longer than one bounded render write (1 MiB UTF-16 units) with a lone surrogate on the chunk
// boundary, where the writer must count it as one unit and not cut it.

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

type surrogateSeg struct {
	T string   `json:"t,omitempty"`
	N int      `json:"n,omitempty"`
	U []uint16 `json:"u,omitempty"`
}

type surrogateCase struct {
	Name           string `json:"name"`
	Mode           string `json:"mode"`
	Cols           int    `json:"cols"`
	Rows           int    `json:"rows"`
	Lines1, Lines2 [][]surrogateSeg
}

func (c surrogateCase) MarshalJSON() ([]byte, error) {
	type plain struct {
		Mode   string           `json:"mode"`
		Cols   int              `json:"cols"`
		Rows   int              `json:"rows"`
		Lines1 [][]surrogateSeg `json:"lines1"`
		Lines2 [][]surrogateSeg `json:"lines2"`
	}
	return json.Marshal(plain{c.Mode, c.Cols, c.Rows, c.Lines1, c.Lines2})
}

func surrogateLine(segs []surrogateSeg) string {
	var b strings.Builder
	for _, s := range segs {
		n := s.N
		if n == 0 {
			n = 1
		}
		b.WriteString(strings.Repeat(s.T, n))
		b.WriteString(jsstring.FromUTF16(s.U))
	}
	return b.String()
}

func surrogateCases() []surrogateCase {
	const H, L = 0xd83d, 0xde00
	t := func(s string, n int) surrogateSeg { return surrogateSeg{T: s, N: n} }
	u := func(units ...uint16) surrogateSeg { return surrogateSeg{U: units} }
	var cases []surrogateCase
	for _, w := range []int{10, 21} {
		rows := map[string][]surrogateSeg{
			"fill+H":            {t("a", w), u(H)},
			"fill+L":            {t("a", w), u(L)},
			"fill-1+H+b":        {t("a", w-1), u(H), t("b", 1)},
			"fill-1+H+bc":       {t("a", w-1), u(H), t("bc", 1)},
			"fill+H+b":          {t("a", w), u(H), t("b", 1)},
			"H+over":            {u(H), t("a", w+3)},
			"sgr fill+L reset":  {t("\x1b[31m", 1), t("a", w), u(L), t("\x1b[0m", 1), t("zz", 1)},
			"wide+H":            {t("日", w/2), u(H)},
			"wide cross H":      {t("日", w/2-1), t("a", 1), u(H), t("日", 1)},
			"LH reversed":       {u(L, H), t("a", w)},
			"two highs":         {t("a", w-2), u(H, H), t("bbb", 1)},
			"emoji+H":           {t("😀", w/2), u(H)},
			"osc8 fill+H":       {t("\x1b]8;;http://x\x07", 1), t("a", w), u(H), t("\x1b]8;;\x07", 1)},
			"tab H":             {t("\t", 1), u(H), t("a", w)},
			"H at col w-1 wide": {t("a", w-1), u(H), t("日", 1)},
		}
		for _, name := range []string{"fill+H", "fill+L", "fill-1+H+b", "fill-1+H+bc", "fill+H+b", "H+over", "sgr fill+L reset", "wide+H", "wide cross H", "LH reversed", "two highs", "emoji+H", "osc8 fill+H", "tab H", "H at col w-1 wide"} {
			row := rows[name]
			for _, mode := range []string{"alt", "main"} {
				cases = append(cases, surrogateCase{
					Name: fmt.Sprintf("%s w=%d %s", mode, w, name), Mode: mode, Cols: w, Rows: 6,
					Lines1: [][]surrogateSeg{{t("head", 1)}, row, {t("tail", 1)}},
					Lines2: [][]surrogateSeg{{t("head", 1)}, row, {t("tail2", 1)}, row},
				})
			}
		}
	}
	// A frame longer than one bounded write (1 MiB UTF-16 units) with a lone surrogate on the chunk boundary.
	const chunk = 1024 * 1024
	for _, at := range []int{-2, -1, 0, 1} {
		big := []surrogateSeg{t("a", chunk-len("\x1b[?2026h")-1+at), u(H), t("b", 10)}
		cases = append(cases, surrogateCase{Name: fmt.Sprintf("main big boundary%+d", at), Mode: "main", Cols: 80, Rows: 6, Lines1: [][]surrogateSeg{big}, Lines2: [][]surrogateSeg{big}})
	}
	return cases
}

func TestSurrogateRowsMatchPiScreens(t *testing.T) {
	cases := surrogateCases()
	input, err := json.Marshal(cases)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/surrogate_screens.mjs", pigversion.UpstreamPackageVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var want []struct {
		Steps []struct {
			Hex    string
			Writes int
			Error  *string
		}
	}
	if err := json.Unmarshal(output, &want); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ResetCapabilitiesCache)
	t.Setenv("TERM", "xterm-256color")
	for _, name := range []string{"TMUX", "ZELLIJ", "STY", "WEZTERM_PANE", "TERM_PROGRAM"} {
		t.Setenv(name, "")
	}
	differ := 0
	for i, c := range cases {
		SetCapabilities(TerminalCapabilities{TrueColor: true, Hyperlinks: true})
		lines := &oracleLines{}
		type stepOut struct {
			hex   string
			error *string
		}
		var got []stepOut
		var out bytes.Buffer
		run := func(fn func()) {
			var e *string
			func() {
				defer func() {
					if v := recover(); v != nil {
						first, _, _ := strings.Cut(fmt.Sprint(v), "\n")
						e = &first
					}
				}()
				fn()
			}()
			got = append(got, stepOut{hex.EncodeToString(out.Bytes()), e})
			out.Reset()
		}
		toLines := func(spec [][]surrogateSeg) []string {
			r := make([]string, len(spec))
			for j, s := range spec {
				r[j] = surrogateLine(s)
			}
			return r
		}
		if c.Mode == "alt" {
			ui := NewTuiAltScreenWithOutput(&out, c.Cols, c.Rows, TuiAltScreenOptions{})
			var pending func()
			ui.afterFunc = func(_ time.Duration, fn func()) stoppableTimer { pending = fn; return stoppedOracleTimer{} }
			ui.Add(lines)
			ui.Start()
			if pending != nil {
				p := pending
				pending = nil
				p()
			}
			out.Reset()
			lines.lines = toLines(c.Lines1)
			run(func() { ui.RenderNow(false) })
			lines.lines = toLines(c.Lines2)
			run(func() { ui.RenderNow(false) })
			run(func() { ui.StopWithOptions(StopOptions{PreserveScreen: false}) })
		} else {
			ui := NewWithOutput(&out, c.Cols, c.Rows)
			ui.SetLogDirectory(t.TempDir())
			ui.Add(lines)
			lines.lines = toLines(c.Lines1)
			run(func() { ui.Render() })
			lines.lines = toLines(c.Lines2)
			run(func() { ui.Render() })
		}
		w := want[i].Steps
		if len(w) != len(got) {
			t.Errorf("%s: %d steps, Pi %d", c.Name, len(got), len(w))
			continue
		}
		for s := range w {
			ge, we := "<none>", "<none>"
			if got[s].error != nil {
				ge = *got[s].error
			}
			if w[s].Error != nil {
				we = *w[s].Error
			}
			if ge != we || got[s].hex != w[s].Hex {
				differ++
				g, _ := hex.DecodeString(got[s].hex)
				p, _ := hex.DecodeString(w[s].Hex)
				at := 0
				for at < len(g) && at < len(p) && g[at] == p[at] {
					at++
				}
				from := max(at-40, 0)
				t.Errorf("%s step %d: error PiG %q Pi %q; first byte difference at %d of %d/%d\n  PiG %q\n  Pi  %q", c.Name, s, ge, we, at, len(g), len(p), g[from:min(at+50, len(g))], p[from:min(at+50, len(p))])
				break
			}
		}
	}
	t.Logf("%d cases, %d differ", len(cases), differ)
}
