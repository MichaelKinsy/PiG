package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/MichaelKinsy/PiG/coding/extension/pigsdk"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

type extensionInitOptions struct {
	path          string
	name          string
	lang          string
	login         bool
	force         bool
	isolated      bool
	jsonOutput    bool
	help          bool
	invalidOption string
}

type extensionInitReport struct {
	Created  bool     `json:"created"`
	Name     string   `json:"name"`
	Path     string   `json:"path"`
	Language string   `json:"language"`
	SDKPath  string   `json:"sdkPath"`
	Files    []string `json:"files,omitempty"`
	Error    string   `json:"error,omitempty"`
}

type scaffoldFile struct {
	rel  string
	body string
	mode os.FileMode
}

var initLanguages = []string{"go", "python", "rust"}

// runExtensionInit scaffolds a minimal, self-contained extension whose
// versioned SDK dependency is resolved by Pig to the staged copy under the
// active config root, so a clean, binary-only host needs no source checkout. Go is fully offline;
// Python needs only python3; Rust needs cargo and (on a cold cache) crates.io
// for transitive dependencies.
func runExtensionInit(args []string) int {
	opts := parseExtensionInitOptions(args)
	if opts.help {
		printExtensionInitHelp()
		return 0
	}
	if opts.invalidOption != "" {
		fmt.Fprintf(os.Stderr, "Unknown option %s for \"extension init\".\n", opts.invalidOption)
		printExtensionInitHelp()
		return 1
	}
	if opts.path == "" {
		printExtensionInitHelp()
		return 1
	}
	if opts.lang == "" {
		opts.lang = "go"
	}
	if !isInitLanguage(opts.lang) {
		return printExtensionInitReport(extensionInitReport{Language: opts.lang, Error: fmt.Sprintf("unsupported --lang %q; supported: %s", opts.lang, strings.Join(initLanguages, ", "))}, opts.jsonOutput)
	}
	if opts.login && opts.lang != "go" {
		return printExtensionInitReport(extensionInitReport{Language: opts.lang, Error: "--login currently supports --lang go only"}, opts.jsonOutput)
	}
	if opts.login && opts.isolated {
		return printExtensionInitReport(extensionInitReport{Language: opts.lang, Error: "--login requires the conventional Go factory form"}, opts.jsonOutput)
	}

	root, err := filepath.Abs(opts.path)
	if err != nil {
		return printExtensionInitReport(extensionInitReport{Path: opts.path, Error: err.Error()}, opts.jsonOutput)
	}
	name := opts.name
	if name == "" {
		name = sanitizeExtensionName(filepath.Base(root))
	}
	if name == "" {
		return printExtensionInitReport(extensionInitReport{Path: root, Error: "cannot derive an extension name; pass --name"}, opts.jsonOutput)
	}
	if name != sanitizeExtensionName(filepath.Base(root)) {
		return printExtensionInitReport(extensionInitReport{Path: root, Name: name, Language: opts.lang, Error: "extension identity comes from the selected directory name; use a matching --name or rename the directory"}, opts.jsonOutput)
	}

	// Stage the SDK for this language so the first Pig-managed cold build is
	// ready even when no interactive session has run under this config root.
	configRoot := codingagent.ConfigRoot()
	if err := pigsdk.EnsureSyncedLang(configRoot, opts.lang); err != nil {
		return printExtensionInitReport(extensionInitReport{Name: name, Path: root, Language: opts.lang, Error: fmt.Sprintf("stage %s SDK: %v", opts.lang, err)}, opts.jsonOutput)
	}
	sdkDir, err := pigsdk.SDKDirFor(configRoot, opts.lang)
	if err != nil {
		return printExtensionInitReport(extensionInitReport{Name: name, Path: root, Language: opts.lang, Error: err.Error()}, opts.jsonOutput)
	}

	files := scaffoldFiles(opts.lang, name, filepath.Base(root), sdkDir, opts.isolated, opts.login)

	if err := os.MkdirAll(root, 0o755); err != nil {
		return printExtensionInitReport(extensionInitReport{Name: name, Path: root, Language: opts.lang, SDKPath: sdkDir, Error: err.Error()}, opts.jsonOutput)
	}
	if !opts.force {
		for _, f := range files {
			if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(f.rel))); err == nil {
				return printExtensionInitReport(extensionInitReport{Name: name, Path: root, Language: opts.lang, SDKPath: sdkDir, Error: fmt.Sprintf("%s already exists; pass --force to overwrite", f.rel)}, opts.jsonOutput)
			}
		}
	}

	written := make([]string, 0, len(files))
	for _, f := range files {
		target := filepath.Join(root, filepath.FromSlash(f.rel))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return printExtensionInitReport(extensionInitReport{Name: name, Path: root, Language: opts.lang, SDKPath: sdkDir, Files: written, Error: err.Error()}, opts.jsonOutput)
		}
		mode := f.mode
		if mode == 0 {
			mode = 0o644
		}
		if err := os.WriteFile(target, []byte(f.body), mode); err != nil {
			return printExtensionInitReport(extensionInitReport{Name: name, Path: root, Language: opts.lang, SDKPath: sdkDir, Files: written, Error: err.Error()}, opts.jsonOutput)
		}
		written = append(written, f.rel)
	}

	report := extensionInitReport{Created: true, Name: name, Path: root, Language: opts.lang, SDKPath: sdkDir, Files: written}
	if opts.jsonOutput {
		return printExtensionInitReport(report, true)
	}
	fmt.Printf("Created %s extension %q in %s\n", opts.lang, name, root)
	for _, rel := range written {
		fmt.Printf("  %s\n", rel)
	}
	fmt.Printf("\nSDK: %s\n", sdkDir)
	fmt.Printf("\nNext steps:\n")
	fmt.Printf("  pig install %s --validate-only --json\n", root)
	fmt.Printf("  pig -e %s          # load it into a session, then edit and /reload\n", root)
	return 0
}

func isInitLanguage(lang string) bool {
	return slices.Contains(initLanguages, lang)
}

func scaffoldFiles(lang, name, dirBase, sdkDir string, isolated, login bool) []scaffoldFile {
	if isolated {
		switch lang {
		case "python":
			return []scaffoldFile{{rel: "main.py", body: pythonStandaloneTemplate(name), mode: 0o755}}
		case "rust":
			return []scaffoldFile{
				{rel: "Cargo.toml", body: rustCargoTemplate(dirBase)},
				{rel: "src/main.rs", body: rustStandaloneTemplate(name)},
				{rel: ".gitignore", body: "target/\n"},
			}
		default:
			return []scaffoldFile{
				{rel: "go.mod", body: goModTemplate(name)},
				{rel: "main.go", body: goStandaloneTemplate(name)},
			}
		}
	}
	switch lang {
	case "python":
		module := strings.ReplaceAll(name, "-", "_")
		if module == "" || module[0] >= '0' && module[0] <= '9' {
			module = "ext_" + module
		}
		return []scaffoldFile{{rel: module + ".py", body: pythonModuleTemplate(name, module), mode: 0o644}}
	case "rust":
		packageName := name
		if packageName == "" || packageName[0] >= '0' && packageName[0] <= '9' {
			packageName = "ext-" + packageName
		}
		return []scaffoldFile{
			{rel: "Cargo.toml", body: rustCargoTemplate(packageName)},
			{rel: "src/lib.rs", body: rustFactoryTemplate(name)},
			{rel: ".gitignore", body: "target/\n"},
		}
	default:
		template := goExtensionTemplate(name)
		if login {
			template = goLoginExtensionTemplate(name)
		}
		return []scaffoldFile{
			{rel: "go.mod", body: goModTemplate(name)},
			{rel: "extension.go", body: template},
		}
	}
}

func parseExtensionInitOptions(args []string) extensionInitOptions {
	opts := extensionInitOptions{}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "-h", "--help":
			opts.help = true
		case "--json":
			opts.jsonOutput = true
		case "--login":
			opts.login = true
		case "--force", "-f":
			opts.force = true
		case "--isolated":
			opts.isolated = true
		case "--name":
			if i+1 < len(args) {
				i++
				opts.name = args[i]
			} else if opts.invalidOption == "" {
				opts.invalidOption = "--name (missing value)"
			}
		case "--lang":
			if i+1 < len(args) {
				i++
				opts.lang = args[i]
			} else if opts.invalidOption == "" {
				opts.invalidOption = "--lang (missing value)"
			}
		default:
			switch {
			case strings.HasPrefix(arg, "--name="):
				opts.name = strings.TrimPrefix(arg, "--name=")
			case strings.HasPrefix(arg, "--lang="):
				opts.lang = strings.TrimPrefix(arg, "--lang=")
			case len(arg) > 0 && arg[0] == '-':
				if opts.invalidOption == "" {
					opts.invalidOption = arg
				}
			case opts.path == "":
				opts.path = arg
			case opts.invalidOption == "":
				opts.invalidOption = arg
			}
		}
	}
	return opts
}

// sanitizeExtensionName lowercases and keeps [a-z0-9-], collapsing anything else
// to a single dash so a directory base name becomes a valid extension name.
func sanitizeExtensionName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	lastDash := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash && b.Len() > 0 {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

// ── Go templates ─────────────────────────────────────────────────────────────

func goModTemplate(name string) string {
	return fmt.Sprintf(`module example.com/%s

go 1.26

// Pig resolves this requirement to the version-matched staged SDK at build time.
require github.com/MichaelKinsy/PiG/extensions/sdk v0.0.0
`, name)
}

func goExtensionTemplate(name string) string {
	packageName := strings.ReplaceAll(name, "-", "_")
	if packageName == "" || packageName[0] >= '0' && packageName[0] <= '9' {
		packageName = "ext_" + packageName
	}
	return fmt.Sprintf(`package %[2]s

import (
	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

func Extension() *sdk.Extension {
	e := sdk.New(%[1]q)

	e.Tool("%[1]s_ping", "Return a short pong so you can confirm the extension is wired up.",
		sdk.Schema{"type": "object", "properties": map[string]any{}},
		func(ctx sdk.Context, params map[string]any) (any, error) {
			return "pong from %[1]s", nil
		})

	e.Command("%[1]s", "Say hello from the %[1]s extension.", func(ctx sdk.Context, args string) error {
		ctx.Notify("hello from %[1]s", "info")
		return nil
	})

	e.OnSessionStart(func(ctx sdk.Context, _ map[string]any) (any, error) {
		ctx.SetStatus(%[1]q, "%[1]s loaded")
		return nil, nil
	})

	return e
}
`, name, packageName)
}

func goStandaloneTemplate(name string) string {
	return fmt.Sprintf(`package main

import sdk "github.com/MichaelKinsy/PiG/extensions/sdk"

func main() {
	ext := sdk.New(%[1]q)
	ext.Tool("%[1]s_ping", "Return a short pong.", sdk.Schema{"type": "object"}, func(sdk.Context, map[string]any) (any, error) {
		return "pong from %[1]s", nil
	})
	if err := ext.Run(); err != nil { panic(err) }
}
`, name)
}

func goLoginExtensionTemplate(name string) string {
	packageName := strings.ReplaceAll(name, "-", "_")
	if packageName == "" || packageName[0] >= '0' && packageName[0] <= '9' {
		packageName = "ext_" + packageName
	}
	brand := []string{
		".........................................",
		".........................................",
		".........................................",
		".........................................",
		".........................................",
	}
	hero := make([]string, 14)
	for i := range hero {
		hero[i] = "................................"
	}
	mascot := []string{
		"................",
		"................",
		".....PPPPPP.....",
		"...PPPPPPPPPP...",
		"..PPPPPPPPPPPP..",
		"..PP..PPPP..PP..",
		"..PP..PPPP..PP..",
		"..PPPPPPPPPPPP..",
		"..PPPP....PPPP..",
		"..PPPPPPPPPPPP..",
		"...PPPPPPPPPP...",
		".....PPPPPP.....",
		"................",
		"................",
	}
	palette := map[string]string{"P": "#67E8F9"}
	return fmt.Sprintf(`package %[2]s

import (
	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

func Extension() *sdk.Extension {
	e := sdk.New(%[1]q)
	e.OnSessionStart(func(ctx sdk.Context, _ map[string]any) (any, error) {
		return nil, ctx.SetLogin(loginDefinition())
	})
	return e
}

func loginDefinition() sdk.LoginDefinition {
	return sdk.LoginDefinition{
		// Replace these grids and text with the identity for your extension.
		Brand: %[3]s,
		Hero: %[4]s,
		Mascot: %[5]s,
		Palette: %[6]s,
		Name: "Custom Extension",
		Description: "Extension-provided login",
		Tagline: "Loaded through the public extension API.",
	}
}
`, name, packageName, goStringSlice(brand), goStringSlice(hero), goStringSlice(mascot), goStringMap(palette))
}

func goStringSlice(values []string) string {
	var b strings.Builder
	b.WriteString("[]string{\n")
	for _, value := range values {
		fmt.Fprintf(&b, "\t\t\t%q,\n", value)
	}
	b.WriteString("\t\t}")
	return b.String()
}

func goStringMap(values map[string]string) string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString("map[string]string{\n")
	for _, key := range keys {
		fmt.Fprintf(&b, "\t\t\t%q: %q,\n", key, values[key])
	}
	b.WriteString("\t\t}")
	return b.String()
}

// ── Python templates ─────────────────────────────────────────────────────────

func pythonModuleTemplate(name, module string) string {
	return fmt.Sprintf(`#!/usr/bin/env python3
"""%[1]s: a pig subprocess extension scaffolded by `+"`pig extension init`"+`.

Pig imports `+"`new_extension`"+` into a generated runner. Pig supplies the staged
SDK on PYTHONPATH.
"""
import pig_sdk


def new_extension() -> pig_sdk.Extension:
    ext = pig_sdk.Extension(%[1]q)

    ext.tool(
        "%[2]s_ping",
        "Return a short pong so you can confirm the extension is wired up.",
        {"type": "object", "properties": {}},
        lambda ctx, args: {"content": "pong from %[1]s"},
    )

    def _hello(ctx, args):
        ctx.notify("hello from %[1]s", "info")

    ext.command("%[1]s", "Say hello from the %[1]s extension.", _hello)

    ext.on_event("session_start", lambda ctx, data: ctx.set_status(%[1]q, "%[1]s loaded"))

    return ext

`, name, module)
}

func pythonStandaloneTemplate(name string) string {
	return fmt.Sprintf(`#!/usr/bin/env python3
import pig_sdk

ext = pig_sdk.Extension(%[1]q)
ext.tool("%[1]s_ping", "Return a short pong.", {"type": "object"}, lambda ctx, args: {"content": "pong from %[1]s"})
ext.run()
`, name)
}

// ── Rust templates ───────────────────────────────────────────────────────────

func rustCargoTemplate(crate string) string {
	return fmt.Sprintf(`[package]
name = %[1]q
version = "0.1.0"
edition = "2024"

[dependencies]
# Pig resolves this dependency to the version-matched staged SDK at build time.
pig-sdk = "0.1.0"
serde_json = "1"
`, crate)
}

func rustFactoryTemplate(name string) string {
	return fmt.Sprintf(`use pig_sdk::{CommandResult, Extension, ToolResult};
use serde_json::{Value, json};

pub fn new_extension() -> Extension {
    let mut ext = Extension::new(%[1]q);
    ext.tool("%[1]s_ping", "Return a short pong.", json!({"type": "object"}), |_ctx, _params: Value| ToolResult::Json(json!({"content": "pong"})));
    ext.command(%[1]q, "Say hello.", |ctx, _args: &str| { ctx.notify("hello", "info"); CommandResult::Ok });
    ext
}
`, name)
}

func rustStandaloneTemplate(name string) string {
	return fmt.Sprintf(`use pig_sdk::{Extension, ToolResult};
use serde_json::{Value, json};

fn main() {
    let mut ext = Extension::new(%[1]q);
    ext.tool("%[1]s_ping", "Return a short pong.", json!({"type": "object"}), |_ctx, _params: Value| ToolResult::Json(json!({"content": "pong"})));
    ext.run().unwrap();
}
`, name)
}

func printExtensionInitReport(report extensionInitReport, jsonOut bool) int {
	if jsonOut {
		data, _ := json.MarshalIndent(report, "", "  ")
		fmt.Println(string(data))
	} else if report.Error != "" {
		fmt.Fprintf(os.Stderr, "pig extension init: %s\n", report.Error)
	}
	if report.Error != "" {
		return 1
	}
	return 0
}

func printExtensionInitHelp() {
	fmt.Print("Usage:\n  pig extension init <path> [--name <name>] [--lang go|python|rust] [--login] [--isolated] [--force] [--json]\n\n" +
		"Scaffold a new extension whose versioned SDK dependency Pig resolves to its\n" +
		"staged copy, so it builds with no Pig source checkout or committed SDK path.\n" +
		"Go builds fully offline; Python needs python3; Rust needs cargo (and crates.io\n" +
		"on a cold cache for transitive dependencies).\n\n" +
		"Options:\n" +
		"  --name <name>   Extension name; it must match the sanitized directory base name.\n" +
		"  --lang <lang>   Language to scaffold: go (default), python, or rust.\n" +
		"  --login         Scaffold a conventional Go login factory with standard PiG art.\n" +
		"  --isolated      Generate an exact standalone instead of a factory.\n" +
		"  --force, -f     Overwrite existing files.\n" +
		"  --json          Machine-readable output.\n")
}
