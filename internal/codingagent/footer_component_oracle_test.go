package codingagent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
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
	for _, width := range []int{1, 2, 3, 4, 5, 8, 12, 20, 40, 80, 120} {
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

	for i, p := range randomFooterProbes(base) {
		add(fmt.Sprintf("random %d", i), p)
	}

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
	cmd := exec.CommandContext(t.Context(), "node", "testdata/footer_component.mjs", pigversion.UpstreamPackageVersion)
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
		// A probe with no session totals, routed model or git branch is also driven through the component's own setters.
		if probe.Totals == (footerJSONTotals{}) && probe.Latest == nil && probe.Routed == nil && probe.Branch == "" {
			component := NewFooterComponent(model, "", nil)
			component.SetCwd(probe.Cwd)
			component.SetName(probe.Name)
			component.SetThinkingLevel(probe.ThinkingLevel)
			component.SetAutoCompactEnabled(probe.Auto)
			component.SetProviderCount(probe.Providers)
			component.SetUsingSubscription(data.usingSubscription)
			for key, text := range probe.Statuses {
				component.SetExtensionStatus(key, text)
			}
			tokens := data.contextTokens
			component.SetContextUsage(&tokens, probe.ContextWindow)
			if viaComponent := component.Render(probe.Width); strings.Join(viaComponent, "\n") != strings.Join(expected[i], "\n") {
				failures++
				if failures <= 6 {
					t.Errorf("%s through the component's setters:\n  Pig %q\n  Pi  %q", names[i], viaComponent, expected[i])
				}
			}
		}
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

// randomFooterProbes draws token counts around every formatTokens boundary, costs, percentages, widths from 1 to 160, home-relative working directories, branches, names and extension statuses with wide, combining and control characters.
func randomFooterProbes(base footerProbe) []footerProbe {
	rng := rand.New(rand.NewSource(20261016))
	counts := []int{0, 1, 9, 99, 999, 1000, 1001, 1049, 1050, 9999, 10000, 10049, 10050, 99999, 100000, 999499, 999500, 999999, 1000000, 1049999, 1050000, 9999999, 10000000, 123456789, 2147483647}
	costs := []float64{0, 0.0004, 0.001, 0.0049, 0.005, 0.01, 0.1, 0.999, 1, 1.005, 12.345, 99.995, 1000.5, 123456.789}
	percents := []float64{0, 0.04, 0.05, 0.1, 1, 33.333, 69.95, 70, 70.04, 89.95, 90, 90.05, 99.99, 100, 100.04, 250}
	texts := []string{"", "x", "main", "feature/long-branch-name-with-many-parts", "日本語", "e\u0301", "😀", "a\tb", "line\nbreak", "\x1b[31mred\x1b[0m", "  padded  ", "very long text that cannot fit within a narrow footer row at all"}
	models := []string{"gpt-4o", "claude-opus-4-1", "日本語モデル", "m", "a-model-with-a-rather-long-identifier-0123456789"}
	providers := []string{"openai", "anthropic", "kimi-coding", "p", "プロバイダ"}
	cwds := []string{"/home/u/project", "/home/u", "/home/u/", "/srv/app", "/home/user2/x", "/", "/home/u/日本語/dir", "relative/dir"}
	levels := []string{"off", "minimal", "low", "medium", "high", "xhigh", "max"}
	var probes []footerProbe
	for range 400 {
		p := base
		p.Model, p.Provider = models[rng.Intn(len(models))], providers[rng.Intn(len(providers))]
		// The harness turns the percentage into a token count, so the windows are large enough for that count to keep the percentage to the digit.
		p.ContextWindow = []int{200000, 1000000, 2000000}[rng.Intn(3)]
		p.Reasoning = rng.Intn(2) == 0
		p.ThinkingLevel = levels[rng.Intn(len(levels))]
		p.Totals = footerJSONTotals{Input: counts[rng.Intn(len(counts))], Output: counts[rng.Intn(len(counts))], CacheRead: counts[rng.Intn(len(counts))], CacheWrite: counts[rng.Intn(len(counts))], Cost: costs[rng.Intn(len(costs))]}
		if rng.Intn(2) == 0 {
			p.Latest = &footerJSONLatest{Input: counts[rng.Intn(len(counts))], CacheRead: counts[rng.Intn(len(counts))], CacheWrite: counts[rng.Intn(len(counts))]}
		}
		if rng.Intn(4) == 0 {
			p.Routed = &footerRouted{ID: models[rng.Intn(len(models))], ContextWindow: []int{1000000, 2000000}[rng.Intn(2)], Level: []string{"", "low", "high"}[rng.Intn(3)]}
		}
		p.Percent = percents[rng.Intn(len(percents))]
		p.Auto = rng.Intn(2) == 0
		p.Providers = 1 + rng.Intn(3)
		p.Subscription = rng.Intn(4) == 0
		p.Cwd = cwds[rng.Intn(len(cwds))]
		p.Branch = texts[rng.Intn(len(texts))]
		p.Name = texts[rng.Intn(len(texts))]
		if rng.Intn(2) == 0 {
			p.Statuses = map[string]string{}
			for range 1 + rng.Intn(3) {
				p.Statuses[texts[rng.Intn(len(texts))]+string(rune('a'+rng.Intn(3)))] = texts[rng.Intn(len(texts))]
			}
		}
		p.Width = 1 + rng.Intn(160)
		p.Experimental = rng.Intn(5) == 0
		probes = append(probes, p)
	}
	return probes
}
