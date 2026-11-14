package extensionconformance

import (
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/tui"
)

// Names that are members of JavaScript's Object.prototype. A lookup table that is a plain JavaScript object answers for them with the inherited member,
// and an assignment to `__proto__` sets the table's prototype instead of an entry (docs/parity/divergences/D645.md). Every SDK looks a name up as an own
// entry: these rows give each of them such a name where a caller gives a name.
var objectPrototypeMemberNames = []string{"constructor", "__proto__", "toString", "hasOwnProperty"}

// ctx.ui.theme with a token named like a member of Object.prototype, through every SDK: fg, getFgAnsi, getBgAnsi and bg throw Pi's
// `Unknown theme color: <token>` (theme.ts keeps its colors in Maps), as for any token the palette does not hold. The palette is the conformance theme,
// whose accent is no SDK's default, and the tokens `accent` and one background are compared with the host's own answers, so an SDK that answered from a
// fallback or that lost the palette fails the row too.
func TestThemeTokenNamedLikeAnObjectPrototypeMemberIsUnknownAcrossSDKs(t *testing.T) {
	eachModelTypesPlacement(t, func(t *testing.T, r *modelTypesRig) {
		t.Cleanup(func() { tui.SetTerminalColors(tui.TerminalColors{}) })
		tui.SetTerminalColors(tui.TerminalColors{})
		theme := conformanceTheme(t, tui.TerminalColorMode256)
		foregrounds, backgrounds := theme.ANSIPalette()
		var background string
		for token := range backgrounds {
			if background == "" || token < background {
				background = token
			}
		}
		if _, ok := foregrounds["accent"]; !ok || background == "" {
			t.Fatalf("the conformance theme has no accent or no background (%d foregrounds, %d backgrounds), so the row proves nothing", len(foregrounds), len(backgrounds))
		}
		for _, name := range objectPrototypeMemberNames {
			if _, ok := foregrounds[name]; ok {
				t.Fatalf("the conformance theme has a color named %s", name)
			}
		}
		r.setTheme(codingagent.ExtensionThemePalette(theme))
		tokens := append(slices.Clone(objectPrototypeMemberNames), "accent", background)
		report := r.toolWith("theme_probe", map[string]any{"fgTokens": tokens, "queries": true, "ansiTokens": tokens, "levels": []string{"off"}, "modifierTexts": themeModifierTexts})
		fgs, _ := report["fgs"].(map[string]any)
		queries, _ := report["queries"].(map[string]any)
		if fgs == nil || queries == nil {
			t.Fatalf("no fgs or no queries in %v", report)
		}
		member := func(key, token string) any {
			values, _ := queries[key].(map[string]any)
			return values[token]
		}
		for _, name := range objectPrototypeMemberNames {
			want := "throw:Unknown theme color: " + name
			for call, got := range map[string]any{"fg": fgs[name], "getFgAnsi": member("fgAnsi", name), "getBgAnsi": member("bgAnsi", name), "bg": member("bgs", name)} {
				if got != want {
					t.Errorf("theme.%s(%q) = %#v, want %q", call, name, got, want)
				}
			}
		}
		for call, pair := range map[string][2]any{
			"fg(accent)":        {fgs["accent"], theme.Fg("accent", "x")},
			"getFgAnsi(accent)": {member("fgAnsi", "accent"), theme.GetFgAnsi("accent")},
			"getBgAnsi":         {member("bgAnsi", background), theme.GetBgAnsi(background)},
			"bg":                {member("bgs", background), theme.Bg(background, "x")},
		} {
			if pair[0] != pair[1] {
				t.Errorf("theme.%s = %#v, want the host's %#v", call, pair[0], pair[1])
			}
		}
	})
}

const nodePrototypeNamesFixture = nodeSyncReads + `
const model = { id: "m", name: "M", reasoning: false, input: ["text"], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 1000, maxTokens: 100 };
const config = (baseUrl) => ({ baseUrl, apiKey: "k", api: "openai-completions", models: [model] });
export default function (pi) {
  // Pi's footer data keeps the statuses in a Map (footer-data-provider.ts): a status under any key is an entry, read back in the same turn.
  pi.registerCommand("status", { handler: async (_args, ctx) => {
    let data;
    ctx.ui.setFooter((_tui, _theme, footerData) => { data = footerData; return { render: () => [], invalidate() {} }; });
    const names = ["plain", "constructor", "toString", "__proto__"];
    for (const name of names) ctx.ui.setStatus(name, "on-" + name);
    line(ctx, "status-set", [...data.getExtensionStatuses()]);
    for (const name of names) ctx.ui.setStatus(name, undefined);
    line(ctx, "status-cleared", [...data.getExtensionStatuses()]);
    ctx.ui.setFooter(undefined);
  } });
  // Pi's model runtime keeps its providers in Maps (model-runtime.ts): a provider of any name is registered, read and unregistered like another, and
  // a name that no provider has is no provider, whatever members the provider states have.
  pi.registerCommand("providers", { handler: async (_args, ctx) => {
    const mr = ctx.modelRegistry;
    const members = () => ["configured", "authStatus", "usingOAuth", "modelsConfig"].map((name) => [mr.hasConfiguredAuth({ provider: name }), mr.getProviderAuthStatus(name)]);
    for (const name of ["sync-plain", "constructor", "__proto__"]) {
      const seen = [reads(mr, name, "m")];
      mr.registerProvider(name, config("http://p.invalid/v1"));
      seen.push(reads(mr, name, "m"), members());
      mr.registerProvider("sync-other", config("http://o.invalid/v1"));
      seen.push(reads(mr, name, "m"), members());
      await mr.refresh({ allowNetwork: false });
      seen.push(reads(mr, name, "m"), mr.getRegisteredProviderIds().includes(name), members());
      mr.unregisterProvider("sync-other");
      mr.unregisterProvider(name);
      await mr.refresh({ allowNetwork: false });
      seen.push(reads(mr, name, "m"), mr.getRegisteredProviderIds().includes(name));
      line(ctx, "provider-" + name, seen);
    }
  } });
}
`

// ctx.ui.setStatus under a key named like a member of Object.prototype, in the Node runtime: the status is an entry of the footer data the extension's
// own footer reads in the same turn, and clearing it removes it, as for the key `plain`.
func TestNodeStatusKeyNamedLikeAnObjectPrototypeMemberIsAStatus(t *testing.T) {
	eachNodeAPIIsolation(t, func(t *testing.T, isolation string) {
		rig := newNodeSyncRig(t, isolation, nil, nodeAPIFixture{"nodeproto-names", nodePrototypeNamesFixture})
		rig.command("nodeproto-names", "status", "")
		if got, want := rig.reported("status-set"), `[["plain","on-plain"],["constructor","on-constructor"],["toString","on-toString"],["__proto__","on-__proto__"]]`; got != want {
			t.Errorf("the statuses after four setStatus calls = %s, want %s", got, want)
		}
		if got := rig.reported("status-cleared"); got != `[]` {
			t.Errorf("the statuses after clearing the four = %s, want []", got)
		}
	})
}

// ctx.modelRegistry with a provider named like a member of Object.prototype, in the Node runtime: registering, re-deriving the view by another
// registration, refreshing and unregistering read exactly as for the provider `sync-plain`, and the names of a provider state's own members are no
// providers while it is registered.
func TestNodeProviderNamedLikeAnObjectPrototypeMemberIsAProvider(t *testing.T) {
	eachNodeAPIIsolation(t, func(t *testing.T, isolation string) {
		rig := newNodeSyncRig(t, isolation, nil, nodeAPIFixture{"nodeproto-names", nodePrototypeNamesFixture})
		rig.command("nodeproto-names", "providers", "")
		plain := rig.reported("provider-sync-plain")
		const unset = `[false,false,false,false,{"configured":false},false]`
		const noMembers = `[[false,{"configured":false}],[false,{"configured":false}],[false,{"configured":false}],[false,{"configured":false}]]`
		if !strings.HasPrefix(plain, `[`+unset+`,[true,true,true,true,`) || !strings.Contains(plain, noMembers) || !strings.HasSuffix(plain, `,`+unset+`,false]`) {
			t.Fatalf("the ordinary provider reads %s, want it unset, then registered in the same turn with no member name a provider, then unset", plain)
		}
		for _, name := range []string{"constructor", "__proto__"} {
			if got := rig.reported("provider-" + name); got != plain {
				t.Errorf("the provider %s reads %s\nwant what the provider sync-plain reads: %s", name, got, plain)
			}
		}
	})
}

// pi.getSettings() in the Node runtime for settings that hold a `__proto__` member (a settings file may: PiG keeps it as one more unknown key, D645):
// the object handed to the extension has no such own member at any depth, so extension code that merges settings member by member sets no prototype
// with it, and nothing under it reads as a setting.
func TestNodeGetSettingsHandsNoProtoMember(t *testing.T) {
	eachNodeAPIIsolation(t, func(t *testing.T, isolation string) {
		rig := newNodeAPIRig(t, isolation, &subprocess.HostCallbacks{GetSettings: func() extension.Settings {
			return extension.Settings{
				"marker":    "node-proto-settings-7",
				"__proto__": map[string]any{"polluted": "yes"},
				"nested":    map[string]any{"a": true, "__proto__": map[string]any{"deep": 1}},
				"list":      []any{map[string]any{"b": 2, "__proto__": map[string]any{"x": 1}}},
			}
		}}, nodeAPIFixture{"nodeproto-settings", nodeAPIPrelude + `
export default function (pi) {
  pi.registerCommand("settings", { handler: async (_args, ctx) => {
    const s = pi.getSettings();
    lines(ctx, "protosettings", Object.hasOwn(s, "__proto__"), Object.hasOwn(s.nested, "__proto__"), Object.hasOwn(s.list[0], "__proto__"), s.polluted, s.nested.deep, JSON.stringify(s));
  } });
}
`})
		rig.command("nodeproto-settings", "settings", "")
		var got string
		waitFor(t, func() bool {
			for _, line := range rig.notifications() {
				if strings.HasPrefix(line, "protosettings|") {
					got = line
					return true
				}
			}
			return false
		})
		if want := `protosettings|false|false|false|undefined|undefined|{"list":[{"b":2}],"marker":"node-proto-settings-7","nested":{"a":true}}`; got != want {
			t.Errorf("pi.getSettings() reports %s\nwant %s", got, want)
		}
	})
}
