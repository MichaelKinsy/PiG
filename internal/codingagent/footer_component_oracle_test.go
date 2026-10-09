package codingagent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"os/exec"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
	"github.com/MichaelKinsy/PiG/tui"
)

type footerProbe struct {
	Model         string            `json:"model"`
	Provider      string            `json:"provider"`
	Reasoning     bool              `json:"reasoning"`
	ContextWindow int               `json:"contextWindow"`
	ThinkingLevel string            `json:"thinkingLevel"`
	Routed        *footerRouted     `json:"routed,omitempty"`
	Totals        footerJSONTotals  `json:"totals"`
	Latest        *footerJSONLatest `json:"latest,omitempty"`
	Percent       float64           `json:"percent"`
	Auto          bool              `json:"auto"`
	Providers     int               `json:"providers"`
	Subscription  bool              `json:"subscription"`
	Cwd           string            `json:"cwd"`
	Home          string            `json:"home"`
	Branch        string            `json:"branch,omitempty"`
	Name          string            `json:"name,omitempty"`
	Statuses      map[string]string `json:"statuses,omitempty"`
	Width         int               `json:"width"`
	Theme         string            `json:"theme"`
	Experimental  bool              `json:"experimental"`
}

type footerRouted struct {
	ID            string `json:"id"`
	ContextWindow int    `json:"contextWindow"`
	Level         string `json:"level,omitempty"`
}

type footerJSONTotals struct {
	Input      int     `json:"input"`
	Output     int     `json:"output"`
	CacheRead  int     `json:"cacheRead"`
	CacheWrite int     `json:"cacheWrite"`
	Cost       float64 `json:"cost"`
}

type footerJSONLatest struct {
	Input      int `json:"input"`
	CacheRead  int `json:"cacheRead"`
	CacheWrite int `json:"cacheWrite"`
}

// footer.ts:render against pinned Pi over a stub session: token and cost stats, the cache hit rate, context percentage colours,
// thinking and routed model, the provider prefix and its fallback, the subscription marker, the home-relative cwd, branch and
// session name, and the sorted extension status row, at several widths.
func TestFooterComponentMatchesPi(t *testing.T) {
	base := footerProbe{Model: "gpt-4o", Provider: "openai", ContextWindow: 128000, ThinkingLevel: "off", Auto: true, Providers: 1,
		Cwd: "/home/u/project", Home: "/home/u", Width: 100, Percent: 12.34}
	vary := func(mutate func(*footerProbe)) footerProbe {
		p := base
		mutate(&p)
		return p
	}
	var probes []footerProbe
	var names []string
	add := func(name string, p footerProbe) {
		probes = append(probes, p)
		names = append(names, name)
	}
	for _, width := range []int{20, 40, 80, 120} {
		w := width
		add(fmt.Sprintf("plain/%d", w), vary(func(p *footerProbe) { p.Width = w }))
		add(fmt.Sprintf("tokens/%d", w), vary(func(p *footerProbe) {
			p.Width = w
			p.Totals = footerJSONTotals{Input: 1234, Output: 99999, CacheRead: 1500000, CacheWrite: 12345678, Cost: 1.23456}
			p.Latest = &footerJSONLatest{Input: 100, CacheRead: 900, CacheWrite: 0}
		}))
		add(fmt.Sprintf("full/%d", w), vary(func(p *footerProbe) {
			p.Width, p.Reasoning, p.ThinkingLevel, p.Providers, p.Branch, p.Name = w, true, "high", 3, "main", "my session"
			p.Totals = footerJSONTotals{Input: 5000, Output: 800, Cost: 0.5}
			p.Routed = &footerRouted{ID: "claude-x", ContextWindow: 200000, Level: "low"}
			p.Statuses = map[string]string{"b": "second", "A": "first\tstatus", "a": "line\none", "ünï": "x"}
		}))
	}
	for _, pct := range []float64{0, 70, 70.1, 90, 90.5, 100, 123.46} {
		add(fmt.Sprintf("percent %v", pct), vary(func(p *footerProbe) { p.Percent = pct }))
		add(fmt.Sprintf("percent %v no auto", pct), vary(func(p *footerProbe) { p.Percent, p.Auto = pct, false }))
	}
	add("thinking off", vary(func(p *footerProbe) { p.Reasoning = true }))
	add("thinking level", vary(func(p *footerProbe) { p.Reasoning, p.ThinkingLevel = true, "xhigh" }))
	add("subscription zero cost", vary(func(p *footerProbe) { p.Subscription = true }))
	add("subscription cost", vary(func(p *footerProbe) { p.Subscription = true; p.Totals.Cost = 2 }))
	add("kimi", vary(func(p *footerProbe) { p.Provider = "kimi-coding" }))
	add("routed no level", vary(func(p *footerProbe) { p.Routed = &footerRouted{ID: "other", ContextWindow: 8000} }))
	add("provider prefix too wide", vary(func(p *footerProbe) { p.Providers, p.Width = 2, 30; p.Totals.Input = 12345 }))
	add("cwd outside home", vary(func(p *footerProbe) { p.Cwd = "/srv/app" }))
	add("cwd is home", vary(func(p *footerProbe) { p.Cwd = "/home/u" }))
	add("cwd sibling of home", vary(func(p *footerProbe) { p.Cwd = "/home/user2/x" }))
	add("no home", vary(func(p *footerProbe) { p.Home = "" }))
	add("status nbsp and formfeed", vary(func(p *footerProbe) { p.Statuses = map[string]string{"k": "a\u00a0b\fc  d\r\ne", "j": "   "} }))
	add("status key order", vary(func(p *footerProbe) {
		p.Statuses = map[string]string{"B": "1", "a": "2", "A": "3", "b": "4", "_": "5", "1": "6", "é": "7", "z": "8", "Z": "9"}
	}))
	add("experimental badge", vary(func(p *footerProbe) { p.Experimental = true }))
	add("experimental badge narrow", vary(func(p *footerProbe) { p.Experimental, p.Width = true, 24 }))
	add("status long", vary(func(p *footerProbe) { p.Width, p.Statuses = 30, map[string]string{"x": strings.Repeat("word ", 20)} }))

	// Every case runs in both themes; the footer's dim, warning and error colours differ between them.
	darkCount := len(probes)
	for i := range darkCount {
		light := probes[i]
		probes[i].Theme, light.Theme = "dark", "light"
		probes = append(probes, light)
		names = append(names, names[i]+" light")
	}
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/footer_component.mjs", pigversion.UpstreamVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected [][]string
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	previousCaps := tui.GetCapabilities()
	t.Cleanup(func() { tui.SetCapabilities(previousCaps) })
	tui.SetCapabilities(tui.TerminalCapabilities{TrueColor: true})
	t.Cleanup(func() { tui.SetTheme("dark") })
	failures := 0
	for i, probe := range probes {
		tui.SetTheme(probe.Theme)
		t.Setenv("PI_EXPERIMENTAL", map[bool]string{true: "1", false: ""}[probe.Experimental])
		t.Setenv("HOME", probe.Home)
		t.Setenv("USERPROFILE", "")
		prov := ai.NewFauxProvider(ai.FauxConfig{ProviderID: probe.Provider})
		model := &ai.Model{ID: probe.Model, Provider: prov, Capabilities: ai.ModelCapabilities{ContextWindow: probe.ContextWindow}}
		if probe.Reasoning {
			model.Capabilities.MaxThinking = ai.ThinkingLevelHigh
		}
		data := footerData{
			model: model, cwd: probe.Cwd, gitBranch: probe.Branch, sessionName: probe.Name,
			thinkingLevel: probe.ThinkingLevel, autoCompactEnabled: probe.Auto, providerCount: probe.Providers,
			usingSubscription: probe.Subscription || probe.Provider == "kimi-coding", extensionStatuses: probe.Statuses,
		}
		limitsWindow := probe.ContextWindow
		if probe.Routed != nil {
			limitsWindow = probe.Routed.ContextWindow
		}
		data.contextTokens = int(math.Round(probe.Percent / 100 * float64(limitsWindow)))
		totals := probe.Totals
		if l := probe.Latest; l != nil {
			totals.Input += l.Input
			totals.CacheRead += l.CacheRead
			totals.CacheWrite += l.CacheWrite
			if prompt := l.Input + l.CacheRead + l.CacheWrite; prompt > 0 {
				rate := float64(l.CacheRead) / float64(prompt) * 100
				data.usage.latestCacheHitRate = &rate
			}
		}
		data.usage.input, data.usage.output, data.usage.cacheRead, data.usage.cacheWrite, data.usage.cost = totals.Input, totals.Output, totals.CacheRead, totals.CacheWrite, totals.Cost
		if r := probe.Routed; r != nil {
			routedModel := &ai.Model{ID: r.ID, Provider: prov, Capabilities: ai.ModelCapabilities{ContextWindow: r.ContextWindow}}
			data.routed = &RoutedModelSelection{Model: routedModel, ThinkingLevel: ai.ModelThinkingLevel(r.Level)}
		}
		got := renderFooter(data, probe.Width)
		if strings.Join(got, "\n") != strings.Join(expected[i], "\n") {
			failures++
			if failures <= 6 {
				t.Errorf("%s:\n  Pig %q\n  Pi  %q", names[i], got, expected[i])
			}
		}
	}
	if failures > 6 {
		t.Errorf("%d of %d probes differ from Pi", failures, len(probes))
	}
}
