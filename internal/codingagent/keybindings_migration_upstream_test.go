package codingagent

// pi: packages/coding-agent/src/migrations.ts

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	jsjson "github.com/MichaelKinsy/PiG/extensions/sdk/json"
	"github.com/MichaelKinsy/PiG/tui"
)

func TestUpstreamKeybindingsMigration(t *testing.T) {
	for _, tc := range []struct{ name, input, want string }{
		// Ports packages/coding-agent/test/keybindings-migration.test.ts:25.
		{"rewrites old key names to namespaced ids", `{"cursorUp":["up","ctrl+p"],"expandTools":"ctrl+x"}`, "{\n  \"tui.editor.cursorUp\": [\n    \"up\",\n    \"ctrl+p\"\n  ],\n  \"app.tools.expand\": \"ctrl+x\"\n}\n"},
		// Ports packages/coding-agent/test/keybindings-migration.test.ts:49.
		{"keeps the namespaced value when old and new names both exist", `{"expandTools":"ctrl+x","app.tools.expand":"ctrl+y"}`, "{\n  \"app.tools.expand\": \"ctrl+y\"\n}\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			file := KeybindingsFile(dir)
			if err := os.WriteFile(file, []byte(tc.input), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, _, err := RunMigrations(t.TempDir(), dir); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			var got, want map[string]any
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal([]byte(tc.want), &want); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("persisted keybindings = %#v, want %#v", got, want)
			}
			if string(data) != tc.want {
				t.Fatalf("persisted bytes = %q, want %q", data, tc.want)
			}
		})
	}
	// Ports packages/coding-agent/test/keybindings-migration.test.ts:72.
	t.Run("loads old key names in memory before the file is rewritten", func(t *testing.T) {
		previous := tui.GetKeybindings()
		t.Cleanup(func() { tui.SetKeybindings(previous) })
		dir := t.TempDir()
		file := KeybindingsFile(dir)
		const input = `{"selectConfirm":"enter","interrupt":"ctrl+x"}`
		if err := os.WriteFile(file, []byte(input), 0o600); err != nil {
			t.Fatal(err)
		}
		kb := NewKeybindingsManager(dir)
		want := map[string][]string{"tui.select.confirm": {"enter"}, "app.interrupt": {"ctrl+x"}}
		if !reflect.DeepEqual(kb.userBindings, want) {
			t.Fatalf("user bindings = %#v, want %#v", kb.userBindings, want)
		}
		effective := kb.merged.GetResolvedBindings()
		if !slices.Equal(effective["tui.select.confirm"], []string{"enter"}) || !slices.Equal(effective["app.interrupt"], []string{"ctrl+x"}) {
			t.Fatalf("effective bindings = %#v", effective)
		}
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != input {
			t.Fatalf("loading in memory rewrote file: %q", data)
		}
	})
}

func TestKeybindingsMigrationPreservesParsedJSONValues(t *testing.T) {
	// Pi migrations.ts:168 writes JSON.stringify(config, null, 2), including UTF-16-sorted extras and nested insertion order.
	dir := t.TempDir()
	path := KeybindingsFile(dir)
	input := `{"cursorUp":"\u0075p","\ue000":{"z":1,"a":"\u003c"},"😀":false}`
	if err := os.WriteFile(path, []byte(input), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := RunMigrations(t.TempDir(), dir); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "{\n  \"tui.editor.cursorUp\": \"up\",\n  \"😀\": false,\n  \"\ue000\": {\n    \"z\": 1,\n    \"a\": \"<\"\n  }\n}\n"
	if string(got) != want {
		t.Fatalf("JSON.stringify rewrite = %q, want %q", got, want)
	}
}

func keybindingsSurrogateCases() []struct{ name, input, want string } {
	// Probed from Pi 0.87.1 core/keybindings.ts:320-358 under Node 24.19.0. JSON.parse retains lone UTF-16 units; extras sort by those units and JSON.stringify escapes them without merging U+FFFD.
	return []struct{ name, input, want string }{
		{
			"high surrogate and replacement",
			`{"cursorUp":"up","\ud800":"high","�":"replacement"}`,
			"{\n  \"tui.editor.cursorUp\": \"up\",\n  \"\\ud800\": \"high\",\n  \"�\": \"replacement\"\n}\n",
		},
		{
			"low surrogate and replacement in reverse order",
			`{"�":"replacement","\udfff":"low","expandTools":"ctrl+x"}`,
			"{\n  \"app.tools.expand\": \"ctrl+x\",\n  \"\\udfff\": \"low\",\n  \"�\": \"replacement\"\n}\n",
		},
		{
			"UTF-16 sorting and paired duplicates",
			`{"cursorUp":"up","\ue000":"private","�":"replacement","\udc00":"low","😀":"first","\ud83d":"high","\ud83d\ude00":"last","\ud800":"first high","":"empty"}`,
			"{\n  \"tui.editor.cursorUp\": \"up\",\n  \"\": \"empty\",\n  \"\\ud800\": \"first high\",\n  \"\\ud83d\": \"high\",\n  \"😀\": \"last\",\n  \"\\udc00\": \"low\",\n  \"\ue000\": \"private\",\n  \"�\": \"replacement\"\n}\n",
		},
		{
			"escaped spelling duplicates and nested opaque values",
			`{"cursor\u0055p":"up","tui.editor.cursorUp":"ctrl+p","x\uD800":{"\ud800":1,"�":2},"x\ud800":["\udfff",null,9007199254740993],"x\\ud800":"literal"}`,
			"{\n  \"tui.editor.cursorUp\": \"ctrl+p\",\n  \"x\\\\ud800\": \"literal\",\n  \"x\\ud800\": [\n    \"\\udfff\",\n    null,\n    9007199254740992\n  ]\n}\n",
		},
	}
}

func TestMarshalMigratedKeybindingsPreservesSurrogateNames(t *testing.T) {
	for _, tc := range keybindingsSurrogateCases() {
		t.Run(tc.name, func(t *testing.T) {
			var raw map[string]json.RawMessage
			if err := jsjson.Unmarshal([]byte(tc.input), &raw); err != nil {
				t.Fatal(err)
			}
			values := make(map[string]any, len(raw))
			for key, value := range raw {
				values[key] = value
			}
			config, migrated := migrateKeybindingsConfig(values)
			if !migrated {
				t.Fatal("legacy binding was not migrated")
			}
			got, err := marshalMigratedKeybindings(config)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tc.want {
				t.Fatalf("JSON.stringify bytes = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRunMigrationsPreservesSurrogatePropertyNames(t *testing.T) {
	// Pi migrations.ts:161-168 parses, migrates, and stringifies the entire object without discarding opaque settings. A second run must leave the file unchanged.
	for _, tc := range keybindingsSurrogateCases() {
		t.Run(tc.name, func(t *testing.T) {
			dir, cwd := t.TempDir(), t.TempDir()
			path := KeybindingsFile(dir)
			if err := os.WriteFile(path, []byte(tc.input), 0o600); err != nil {
				t.Fatal(err)
			}
			for range 2 {
				if _, _, err := RunMigrations(cwd, dir); err != nil {
					t.Fatal(err)
				}
				got, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if string(got) != tc.want {
					t.Fatalf("persisted bytes = %q, want %q", got, tc.want)
				}
			}
		})
	}
}

func surrogateKeybindingsStressJSON() (input, want string) {
	var in, out strings.Builder
	in.WriteString(`{"cursorUp":"up","�":-1`)
	out.WriteString("{\n  \"tui.editor.cursorUp\": \"up\",\n")
	// The complete UTF-16 surrogate range is independent of the implementation's decoded property count. Descending input forces the migration to sort every retained unit.
	for unit := 0xdfff; unit >= 0xd800; unit-- {
		fmt.Fprintf(&in, `,"\u%04x":%d`, unit, unit)
	}
	for unit := 0xd800; unit <= 0xdfff; unit++ {
		fmt.Fprintf(&out, "  \"\\u%04x\": %d,\n", unit, unit)
	}
	in.WriteByte('}')
	out.WriteString("  \"�\": -1\n}\n")
	return in.String(), out.String()
}

func TestRunMigrationsPreservesEverySurrogatePropertyName(t *testing.T) {
	// Pi core/keybindings.ts:351-358 sorts extras without decoding lone surrogates into Unicode replacement characters.
	dir, cwd := t.TempDir(), t.TempDir()
	path := KeybindingsFile(dir)
	input, want := surrogateKeybindingsStressJSON()
	if err := os.WriteFile(path, []byte(input), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := RunMigrations(cwd, dir); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("all-surrogate migration differs from UTF-16-ordered JSON: got %d bytes, want %d", len(got), len(want))
	}
}

func BenchmarkRunKeybindingsMigration(b *testing.B) {
	stress, _ := surrogateKeybindingsStressJSON()
	for _, tc := range []struct{ name, input string }{
		{"ordinary", `{"cursorUp":["up","ctrl+p"],"expandTools":"ctrl+x","app.tools.expand":"ctrl+y","selectConfirm":"enter"}`},
		{"all_surrogate_names", stress},
	} {
		b.Run(tc.name, func(b *testing.B) {
			dir, cwd := b.TempDir(), b.TempDir()
			path := KeybindingsFile(dir)
			input := []byte(tc.input)
			b.ReportAllocs()
			for b.Loop() {
				if err := os.WriteFile(path, input, 0o600); err != nil {
					b.Fatal(err)
				}
				if _, _, err := RunMigrations(cwd, dir); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func TestKeybindingsMigrationLeavesUnchangedFilesAlone(t *testing.T) {
	// Pi migrations.ts:162-169 ignores non-objects and malformed JSON, and only writes migrated objects.
	for _, input := range []string{"null", "[]", `"text"`, `{`, `{}`, `{"app.tools.expand":"ctrl+y"}`, `{"\ud800":"opaque","�":"replacement"}`} {
		t.Run(input, func(t *testing.T) {
			dir := t.TempDir()
			path := KeybindingsFile(dir)
			if err := os.WriteFile(path, []byte(input), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, _, err := RunMigrations(t.TempDir(), dir); err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != input {
				t.Fatalf("unchanged file rewritten: %q", got)
			}
		})
	}
}

func TestKeybindingsMigrationBOMAndDefinitionOrder(t *testing.T) {
	// Pi migrations.ts:161 strips BOM; core/keybindings.ts:342 orders known keys before sorted extras.
	dir := t.TempDir()
	path := KeybindingsFile(dir)
	input := "\ufeff" + `{"z-extra":"ctrl+z","expandTools":"ctrl+x","selectConfirm":"enter","cursorUp":"up","a-extra":"<"}`
	if err := os.WriteFile(path, []byte(input), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := RunMigrations(t.TempDir(), dir); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "{\n  \"tui.editor.cursorUp\": \"up\",\n  \"tui.select.confirm\": \"enter\",\n  \"app.tools.expand\": \"ctrl+x\",\n  \"a-extra\": \"<\",\n  \"z-extra\": \"ctrl+z\"\n}\n"
	if string(got) != want {
		t.Fatalf("migrated bytes = %q, want %q", got, want)
	}
}
