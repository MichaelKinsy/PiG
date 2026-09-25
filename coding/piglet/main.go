// Package piglet provides piglet-based capability scoping for pig sessions.
//
// D18: Stock Pig's generic `pig piglet` command and in-session capability
// scoping.
//
// The package has two entry points:
//   - RunCommand: pre-session CLI (pig piglet list|show|validate|schema|add|remove)
//   - BuildExtension: in-session runtime scoping via extension.Extension
//
// The extension reads a piglet YAML (from --piglet flag, PIG_PIGLET_PATH
// env, PIG_PIGLET_NAME env, or search paths), parses per-source tool
// scoping rules, and calls SetActiveTools to filter the tool set before
// the first agent turn.
//
// Piglet is used by:
//   - managed runtimes (mounted YAML through PIG_PIGLET_PATH)
//   - local Pig invocations (--piglet flag or ~/.pig/piglets/<name>.yaml)
package piglet

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/installresolver"
	sourceref "github.com/MichaelKinsy/PiG/coding/source"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// ── Pre-session CLI ──────────────────────────────────────────────────────────

// RunCommand handles `pig piglet <subcommand>`.
// Returns -1 if args do not match, 0 on success, and 1+ on error.
func RunCommand(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "piglet" {
		return -1
	}
	sub := ""
	rest := args[1:]
	if len(rest) > 0 {
		sub = rest[0]
		rest = rest[1:]
	}

	switch sub {
	case "", "-h", "--help":
		printHelp(stdout)
		return 0
	case "list":
		return cmdList(rest, stdout, stderr)
	case "show":
		return cmdShow(rest, stdout, stderr)
	case "validate":
		return cmdValidate(rest, stdout, stderr)
	case "schema":
		return cmdSchema(rest, stdout, stderr)
	case "add":
		return cmdAdd(rest, stdout, stderr)
	case "pull":
		return cmdPull(rest, stdout, stderr)
	case "remove":
		return cmdRemove(rest, stdout, stderr)
	case "keygen":
		return cmdKeygen(rest, stdout, stderr)
	case "trust":
		return cmdTrust(rest, stdout, stderr)
	case "verify":
		return cmdVerify(rest, stdout, stderr)
	default:
		_, _ = fmt.Fprintf(stderr, "Unknown piglet command %q.\n", sub)
		printHelp(stderr)
		return 1
	}
}

func printHelp(w io.Writer) {
	_, _ = fmt.Fprint(w, `Usage:
  pig piglet list [--json]         List source and Piglet Binary facets
  pig piglet show <name>           Show source/effective Piglet details and records
  pig piglet show --record <path>   Validate and show a Piglet record
  pig piglet validate <name|path>  Validate schema and resolve every origin
  pig piglet schema                Print the published Piglet JSON Schema v1
  pig piglet add <path|npm:ref|git:ref>
                                      Install one validated Piglet source
  pig piglet pull <release-ref>      Install one signed Piglet Binary release
  pig piglet publish <name|path> --to github --repo <owner/repo> --sign-key <key> [--yes]
                                      Dry-run or publish signed Binaries to GitHub Releases
  pig piglet remove <name> [facet] Remove --source, --binary, or --all
  pig piglet build <name> --format <script|binary|image> --out <destination> [--sign-key <key>]
                                      Write a source script or build an artifact
  pig piglet keygen <key-path>     Create an ed25519 Piglet Binary signing key pair
  pig piglet verify <binary>       Check a Piglet Binary's signature without running it
  pig piglet trust [list|add|revoke|require]
                                      Manage trusted Piglet Binary signer keys
                                      (image, --locked, and --record are reserved)

Catalog refs require a Piglet source resolver supplied by an installed product.

Piglets are discovered from (in order):
  .pig/piglets/         workspace
  ~/.pig/piglets/       user
  /etc/pig/piglets/     system
`)
}

type pigletListOutput struct {
	Piglets []pigletListItem `json:"piglets"`
}

type pigletListItem struct {
	Name        string             `json:"name"`
	Scope       string             `json:"scope"`
	Source      string             `json:"source,omitempty"`
	Description string             `json:"description,omitempty"`
	Origin      *pigletOrigin      `json:"origin,omitempty"`
	Records     []pigletRecordJSON `json:"records"`
}

func cmdList(args []string, stdout, stderr io.Writer) int {
	jsonMode := false
	for _, arg := range args {
		switch arg {
		case "--json":
			jsonMode = true
		case "--no-input":
		case "-h", "--help":
			_, _ = fmt.Fprintln(stdout, "Usage: pig piglet list [--json] [--no-input]")
			return 0
		default:
			_, _ = fmt.Fprintln(stderr, "Usage: pig piglet list [--json] [--no-input]")
			return 2
		}
	}
	piglets, err := List()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "error: Piglet inventory is invalid: %v\n", err)
		return 1
	}
	if jsonMode {
		items := make([]pigletListItem, len(piglets))
		for i, p := range piglets {
			var origin *pigletOrigin
			if p.Path != "" {
				origin, err = readPigletOrigin(p.Path)
				if err != nil {
					_, _ = fmt.Fprintf(stderr, "error: Piglet inventory is invalid: %v\n", err)
					return 1
				}
			}
			items[i] = pigletListItem{Name: p.Name, Scope: p.Location, Source: p.Path, Description: p.Description, Origin: origin, Records: recordJSON(p.Records)}
		}
		return renderPigletListJSON(pigletListOutput{Piglets: items}, stdout, stderr)
	}
	if len(piglets) == 0 {
		_, _ = fmt.Fprintln(stdout, "No piglets found.")
		return 0
	}

	w := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "NAME\tSCOPE\tSOURCE\tBINARY\tIMAGE\tDESCRIPTION")
	for _, p := range piglets {
		source := p.Path
		if source == "" {
			source = "-"
		}
		binary := "-"
		if len(p.Records) > 0 {
			targets := make([]string, len(p.Records))
			for i, record := range p.Records {
				targets[i] = record.Target
			}
			binary = strings.Join(targets, ",")
		}
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t-\t%s\n", p.Name, p.Location, source, binary, p.Description)
	}
	_ = w.Flush()
	return 0
}

type pigletShowOutput struct {
	Name      string             `json:"name"`
	Source    string             `json:"source,omitempty"`
	Piglet    any                `json:"piglet,omitempty"`
	Effective bool               `json:"effective,omitempty"`
	Records   []pigletRecordJSON `json:"records"`
}

type pigletRecordJSON struct {
	Path                string                `json:"path"`
	ResolutionPath      string                `json:"resolutionPath"`
	ArtifactPath        string                `json:"artifactPath"`
	PigletDigest        string                `json:"pigletDigest"`
	ReleaseVersion      string                `json:"releaseVersion,omitempty"`
	Target              string                `json:"target"`
	ArtifactDigest      string                `json:"artifactDigest"`
	Verified            bool                  `json:"verified"`
	Signature           string                `json:"signature"`
	ComponentPlanDigest string                `json:"componentPlanDigest,omitempty"`
	ResolutionDigest    string                `json:"resolutionDigest,omitempty"`
	BinaryDigest        string                `json:"binaryDigest,omitempty"`
	Components          []pigletComponentJSON `json:"components,omitempty"`
}

type pigletComponentJSON struct {
	Kind            string `json:"kind"`
	Name            string `json:"name"`
	Realization     string `json:"realization"`
	Materialization string `json:"materialization"`
}

func renderPigletListJSON(output pigletListOutput, stdout, stderr io.Writer) int {
	data, err := json.Marshal(output)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "error: encode Piglet list JSON: %v\n", err)
		return 1
	}
	_, _ = fmt.Fprintln(stdout, string(data))
	return 0
}

func cmdShow(args []string, stdout, stderr io.Writer) int {
	jsonMode := false
	effectiveMode := false
	nameOrPath := ""
	recordPath := ""
	workspace := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--json":
			jsonMode = true
		case "--effective":
			effectiveMode = true
		case "--workspace":
			if i+1 >= len(args) {
				_, _ = fmt.Fprintln(stderr, "error: --workspace requires a directory")
				return 2
			}
			i++
			workspace = args[i]
		case "--record":
			if i+1 >= len(args) {
				_, _ = fmt.Fprintln(stderr, "Usage: pig piglet show <name|path> [--effective] [--workspace <dir>] [--json] | pig piglet show --record <path> [--json]")
				return 2
			}
			i++
			recordPath = args[i]
		case "-h", "--help":
			_, _ = fmt.Fprintln(stdout, "Usage: pig piglet show <name|path> [--effective] [--workspace <dir>] [--json] | pig piglet show --record <path> [--json]")
			return 0
		default:
			if strings.HasPrefix(args[i], "-") || nameOrPath != "" {
				_, _ = fmt.Fprintln(stderr, "Usage: pig piglet show <name|path> [--effective] [--workspace <dir>] [--json] | pig piglet show --record <path> [--json]")
				return 2
			}
			nameOrPath = args[i]
		}
	}
	if recordPath != "" {
		if nameOrPath != "" || effectiveMode {
			_, _ = fmt.Fprintln(stderr, "error: --record cannot be combined with a Piglet name or --effective")
			return 2
		}
		return showRecordFile(recordPath, jsonMode, stdout, stderr)
	}
	if nameOrPath == "" {
		_, _ = fmt.Fprintln(stderr, "Usage: pig piglet show <name|path> [--effective] [--workspace <dir>] [--json]")
		return 2
	}

	inventory, err := List()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "error: Piglet inventory is invalid: %v\n", err)
		return 1
	}
	var info *PigletInfo
	for i := range inventory {
		if inventory[i].Name == nameOrPath {
			info = &inventory[i]
			break
		}
	}
	resolved, err := Resolve(nameOrPath)
	if err != nil {
		if info != nil && info.Path == "" && len(info.Records) > 0 {
			return renderBinaryOnlyPiglet(*info, jsonMode, stdout, stderr)
		}
		_, _ = fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	p, err := Parse(resolved)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	if info == nil || info.Name != p.Name {
		for i := range inventory {
			if inventory[i].Name == p.Name {
				info = &inventory[i]
				break
			}
		}
	}
	var records []RecordInfo
	if info != nil {
		records = info.Records
	}
	if effectiveMode {
		if workspace == "" {
			workspace, _ = os.Getwd()
		}
		resolution, resolveErr := ResolveEffectiveWithOptions(resolved, ResolveOptions{Workspace: workspace})
		if resolveErr != nil {
			_, _ = fmt.Fprintf(stderr, "error: resolve effective Piglet: %v\n", resolveErr)
			return 1
		}
		p, err = pigletWithEffectiveDefaults(resolution.Piglet)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "error: resolve effective Piglet: %v\n", err)
			return 1
		}
	}
	if jsonMode {
		pigletValue, err := pigletJSONValue(p)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "error: encode Piglet JSON: %v\n", err)
			return 1
		}
		return renderPigletShowJSON(pigletShowOutput{Name: p.Name, Source: resolved, Piglet: pigletValue, Effective: effectiveMode, Records: recordJSON(records)}, stdout, stderr)
	}

	// Human-readable output
	_, _ = fmt.Fprintf(stdout, "Piglet: %s\n", p.Name)
	if p.Description != "" {
		_, _ = fmt.Fprintf(stdout, "  %s\n", p.Description)
	}
	_, _ = fmt.Fprintf(stdout, "Source: %s\n", resolved)

	switch {
	case p.BuiltinTools == nil:
		_, _ = fmt.Fprintln(stdout, "Built-in tools: all (default)")
	case len(*p.BuiltinTools) == 0:
		_, _ = fmt.Fprintln(stdout, "Built-in tools: none")
	default:
		_, _ = fmt.Fprintf(stdout, "Built-in tools: %s\n", strings.Join(*p.BuiltinTools, ", "))
	}

	if len(p.Packages) > 0 {
		_, _ = fmt.Fprintln(stdout, "\nPackages:")
		for _, alias := range slices.Sorted(maps.Keys(p.Packages)) {
			_, _ = fmt.Fprintf(stdout, "  %s  source=%s\n", alias, p.Packages[alias])
		}
	}

	if len(p.Extensions) > 0 {
		_, _ = fmt.Fprintln(stdout, "\nExtensions:")
		for _, ext := range p.Extensions {
			scope := "all"
			if ext.Tools != nil {
				if len(*ext.Tools) == 0 {
					scope = "none"
				} else {
					scope = strings.Join(*ext.Tools, ", ")
				}
			}
			origin := ""
			if len(ext.Origins) > 0 {
				origin = "  origin=" + ext.Origins[0]
			}
			_, _ = fmt.Fprintf(stdout, "  %s  tools=%s%s\n", ext.Name, scope, origin)
		}
	}

	if len(p.Skills) > 0 {
		_, _ = fmt.Fprintln(stdout, "\nSkills:")
		for _, s := range p.Skills {
			origin := "(no origin)"
			if len(s.Origins) > 0 {
				origin = s.Origins[0]
			}
			_, _ = fmt.Fprintf(stdout, "  %s  origin=%s\n", s.Name, origin)
		}
	}

	if p.SystemPrompt != nil {
		if p.SystemPrompt.File != "" {
			_, _ = fmt.Fprintf(stdout, "\nSystem prompt: file=%s\n", p.SystemPrompt.File)
		} else if p.SystemPrompt.Text != "" {
			preview := p.SystemPrompt.Text
			if idx := strings.Index(preview, "\n"); idx > 0 {
				preview = preview[:idx]
			}
			if len(preview) > 80 {
				preview = preview[:77] + "..."
			}
			_, _ = fmt.Fprintf(stdout, "\nSystem prompt: %s (%d bytes)\n", preview, len(p.SystemPrompt.Text))
		}
	}

	if p.AgentEnv != nil {
		_, _ = fmt.Fprintln(stdout, "\nAgent environment:")
		switch {
		case p.AgentEnv.Image != "":
			_, _ = fmt.Fprintf(stdout, "  image: %s\n", p.AgentEnv.Image)
		case p.AgentEnv.DevContainer != "":
			_, _ = fmt.Fprintf(stdout, "  devContainer: %s\n", p.AgentEnv.DevContainer)
		case p.AgentEnv.Source != "":
			_, _ = fmt.Fprintf(stdout, "  source: %s\n", p.AgentEnv.Source)
		}
		if p.AgentEnv.PigRuntime != nil {
			_, _ = fmt.Fprintf(stdout, "  Pig runtime: %s@%s\n", p.AgentEnv.PigRuntime.Mode, p.AgentEnv.PigRuntime.Version)
		}
		if p.AgentEnv.Policy != nil {
			_, _ = fmt.Fprintf(stdout, "  policy: %s\n", p.AgentEnv.Policy.Preset)
		}
	}

	if p.Model != nil {
		_, _ = fmt.Fprintln(stdout, "\nModel:")
		if p.Model.Provider != "" {
			_, _ = fmt.Fprintf(stdout, "  provider: %s\n", p.Model.Provider)
		}
		if p.Model.Name != "" {
			_, _ = fmt.Fprintf(stdout, "  name: %s\n", p.Model.Name)
		}
	}

	if p.Build != nil {
		_, _ = fmt.Fprintln(stdout, "\nBuild defaults:")
		if len(p.Build.Targets) > 0 {
			_, _ = fmt.Fprintf(stdout, "  targets: %s\n", strings.Join(p.Build.Targets, ", "))
		}
		if p.Build.OutputName != "" {
			_, _ = fmt.Fprintf(stdout, "  output: %s\n", p.Build.OutputName)
		}
	}
	if p.Release != nil && p.Release.Version != "" {
		_, _ = fmt.Fprintf(stdout, "\nRelease:\n  version: %s\n", p.Release.Version)
	}

	renderRecordSummary(stdout, records)
	return 0
}

func pigletWithEffectiveDefaults(p *Piglet) (*Piglet, error) {
	data, err := yaml.Marshal(p)
	if err != nil {
		return nil, err
	}
	effective, err := ParseBytes(data)
	if err != nil {
		return nil, err
	}
	effective.AgentEnv = p.EffectiveAgentEnvironment()
	return effective, nil
}

func pigletJSONValue(p *Piglet) (any, error) {
	data, err := yaml.Marshal(p)
	if err != nil {
		return nil, err
	}
	var value any
	if err := yaml.Unmarshal(data, &value); err != nil {
		return nil, err
	}
	if document, ok := value.(map[string]any); ok && len(p.Secrets) > 0 {
		secrets := make([]map[string]string, 0, len(p.Secrets))
		for _, declaration := range p.Secrets {
			secrets = append(secrets, map[string]string{"name": declaration.Name, "source": secretSourceKind(declaration.From)})
		}
		document["secrets"] = secrets
	}
	return value, nil
}

func secretSourceKind(source SecretSource) string {
	switch {
	case source.Env != "":
		return "env"
	case source.File != "":
		return "file"
	case source.Ref != "":
		return "ref"
	default:
		return "unknown"
	}
}

func recordJSON(records []RecordInfo) []pigletRecordJSON {
	output := make([]pigletRecordJSON, len(records))
	for i, record := range records {
		output[i] = pigletRecordJSON{
			Path: record.Path, ResolutionPath: record.ResolutionPath, ArtifactPath: record.ArtifactPath, PigletDigest: record.PigletDigest, ReleaseVersion: record.ReleaseVersion,
			Target: record.Target, ArtifactDigest: record.ArtifactDigest, Verified: record.VerificationOK, Signature: signatureSummary(record),
			ComponentPlanDigest: record.ComponentPlanDigest, ResolutionDigest: record.ResolutionDigest, BinaryDigest: record.BinaryDigest,
			Components: componentJSON(record.Components),
		}
	}
	return output
}

func componentJSON(components []RecordComponent) []pigletComponentJSON {
	if len(components) == 0 {
		return nil
	}
	output := make([]pigletComponentJSON, len(components))
	for i, component := range components {
		output[i] = pigletComponentJSON(component)
	}
	return output
}

func renderPigletShowJSON(output pigletShowOutput, stdout, stderr io.Writer) int {
	data, err := json.MarshalIndent(output, "", "  ")
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "error: encode Piglet JSON: %v\n", err)
		return 1
	}
	_, _ = fmt.Fprintln(stdout, string(data))
	return 0
}

func renderBinaryOnlyPiglet(info PigletInfo, jsonMode bool, stdout, stderr io.Writer) int {
	if jsonMode {
		return renderPigletShowJSON(pigletShowOutput{Name: info.Name, Records: recordJSON(info.Records)}, stdout, stderr)
	}
	_, _ = fmt.Fprintf(stdout, "Piglet: %s\nSource: none (Piglet Binary only)\n", info.Name)
	renderRecordSummary(stdout, info.Records)
	return 0
}

func renderRecordSummary(stdout io.Writer, records []RecordInfo) {
	if len(records) == 0 {
		return
	}
	_, _ = fmt.Fprintln(stdout, "\nPiglet Binaries:")
	for _, record := range records {
		_, _ = fmt.Fprintf(stdout, "  %s  version=%s  build-checks-passed=%t\n    record=%s\n    artifact=%s (%s)\n    signature: %s\n",
			record.Target, record.ReleaseVersion, record.VerificationOK, record.Path, record.ArtifactPath, record.ArtifactDigest, signatureSummary(record))
		renderComponentSummary(stdout, "    ", record.Components)
	}
}

func renderComponentSummary(stdout io.Writer, indent string, components []RecordComponent) {
	if len(components) == 0 {
		_, _ = fmt.Fprintf(stdout, "%scomponents: none (Resource-only Piglet)\n", indent)
		return
	}
	_, _ = fmt.Fprintf(stdout, "%scomponents:\n", indent)
	for _, component := range components {
		_, _ = fmt.Fprintf(stdout, "%s  %s/%s  %s  %s\n", indent, component.Kind, component.Name, component.Realization, component.Materialization)
	}
}

func showRecordFile(path string, jsonMode bool, stdout, stderr io.Writer) int {
	record, err := readRecordFile(path)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	if jsonMode {
		data, err := json.MarshalIndent(record, "", "  ")
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "error: encode Piglet record %s: %v\n", path, err)
			return 1
		}
		_, _ = fmt.Fprintln(stdout, string(data))
		return 0
	}
	_, _ = fmt.Fprintf(stdout, "Piglet record: %s\nKind: %s\nPiglet: %s\nVersion: %s\nDigest: %s\n",
		path, record.Kind, record.Piglet, record.ReleaseVersion, record.Digest)
	if record.Resolution != nil {
		_, _ = fmt.Fprintf(stdout, "Source digest: %s\nEffective digest: %s\nComponent plan: %s\n",
			record.Resolution.SourceDigest, record.Resolution.EffectiveDigest, record.Resolution.ComponentPlan.Digest)
		renderComponentSummary(stdout, "  ", recordComponents(&record.Resolution.ComponentPlan))
	}
	if record.Binary != nil {
		_, _ = fmt.Fprintf(stdout, "Resolution: %s\nTarget: %s\nArtifact digest: %s\nVerified: %t\n",
			record.Binary.ResolutionDigest, record.Binary.Target, record.Binary.Artifact.Digest, record.Binary.Verification.Passed)
	}
	return 0
}

type pigletValidationOutput struct {
	Valid            bool                       `json:"valid"`
	Name             string                     `json:"name,omitempty"`
	Source           string                     `json:"source,omitempty"`
	Extensions       []pigletResolvedResource   `json:"extensions"`
	Skills           []pigletResolvedResource   `json:"skills"`
	AgentEnvironment *pigletResolvedEnvironment `json:"agentEnvironment,omitempty"`
	Errors           []string                   `json:"errors"`
}

type pigletResolvedResource struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

type pigletResolvedEnvironment struct {
	Form           string `json:"form"`
	Value          string `json:"value"`
	RuntimeMode    string `json:"runtimeMode"`
	RuntimeVersion string `json:"runtimeVersion"`
	PolicyPreset   string `json:"policyPreset"`
}

// cmdSchema prints the published closed Piglet JSON Schema v1. It takes no
// arguments and never reads Piglet state.
func cmdSchema(args []string, stdout, stderr io.Writer) int {
	if len(args) == 1 && (args[0] == "-h" || args[0] == "--help") {
		_, _ = fmt.Fprintln(stdout, "Usage: pig piglet schema")
		return 0
	}
	if len(args) > 0 {
		_, _ = fmt.Fprintf(stderr, "pig piglet schema takes no arguments; got %q\n", args[0])
		return 2
	}
	_, _ = stdout.Write(SchemaJSON())
	return 0
}

func cmdValidate(args []string, stdout, stderr io.Writer) int {
	jsonMode := false
	nameOrPath := ""
	workspace := ""
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "--json":
			jsonMode = true
		case "--no-input":
		case "--workspace":
			if i+1 >= len(args) {
				_, _ = fmt.Fprintln(stderr, "error: --workspace requires a directory")
				return 2
			}
			i++
			workspace = args[i]
		case "-h", "--help":
			_, _ = fmt.Fprintln(stdout, "Usage: pig piglet validate <name-or-path> [--workspace <dir>] [--json] [--no-input]")
			return 0
		default:
			if strings.HasPrefix(arg, "-") || nameOrPath != "" {
				_, _ = fmt.Fprintln(stderr, "Usage: pig piglet validate <name-or-path> [--workspace <dir>] [--json] [--no-input]")
				return 2
			}
			nameOrPath = arg
		}
	}
	if nameOrPath == "" {
		_, _ = fmt.Fprintln(stderr, "Usage: pig piglet validate <name-or-path> [--workspace <dir>] [--json] [--no-input]")
		return 2
	}
	output := pigletValidationOutput{Extensions: []pigletResolvedResource{}, Skills: []pigletResolvedResource{}, Errors: []string{}}
	resolved, err := Resolve(nameOrPath)
	if err != nil {
		output.Errors = append(output.Errors, err.Error())
		return renderPigletValidation(output, jsonMode, stdout, stderr)
	}
	output.Source = resolved
	if workspace == "" {
		workspace, _ = os.Getwd()
	}
	resolution, err := ResolveEffectiveWithOptions(resolved, ResolveOptions{Workspace: workspace})
	if err != nil {
		output.Errors = append(output.Errors, err.Error())
		return renderPigletValidation(output, jsonMode, stdout, stderr)
	}
	p := resolution.Piglet
	output.Name = p.Name
	resolvedExtensions, extensionErrors := ResolveExtensions(p)
	for _, resolutionErr := range extensionErrors {
		output.Errors = append(output.Errors, resolutionErr.Error())
	}
	for _, resolved := range resolvedExtensions {
		output.Extensions = append(output.Extensions, pigletResolvedResource{Name: resolved.Entry.Name, Path: resolved.Path})
	}
	resolvedSkills, skillErrors := ResolveSkills(p)
	for _, resolutionErr := range skillErrors {
		output.Errors = append(output.Errors, resolutionErr.Error())
	}
	for _, resolved := range resolvedSkills {
		output.Skills = append(output.Skills, pigletResolvedResource{Name: resolved.Entry.Name, Path: resolved.Path})
	}
	for _, skill := range p.Skills {
		if skill.Content != "" {
			output.Skills = append(output.Skills, pigletResolvedResource{Name: skill.Name, Path: "<inline>"})
		}
	}
	resolvedEnvironment, environmentErr := ResolveAgentEnvironment(p)
	if environmentErr != nil {
		output.Errors = append(output.Errors, environmentErr.Error())
	} else if resolvedEnvironment != nil {
		effective := p.EffectiveAgentEnvironment()
		output.AgentEnvironment = &pigletResolvedEnvironment{
			Form: resolvedEnvironment.Form, Value: resolvedEnvironment.Value,
			RuntimeMode: effective.PigRuntime.Mode, RuntimeVersion: effective.PigRuntime.Version, PolicyPreset: effective.Policy.Preset,
		}
		if err := p.ValidateAgentEnvironmentLaunch(); err != nil {
			output.Errors = append(output.Errors, err.Error())
		}
	}
	output.Valid = len(output.Errors) == 0
	return renderPigletValidation(output, jsonMode, stdout, stderr)
}

func renderPigletValidation(output pigletValidationOutput, jsonMode bool, stdout, stderr io.Writer) int {
	switch {
	case jsonMode:
		data, err := json.Marshal(output)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "error: encode Piglet validation JSON: %v\n", err)
			return 1
		}
		_, _ = fmt.Fprintln(stdout, string(data))
	case output.Valid:
		for _, resource := range output.Extensions {
			_, _ = fmt.Fprintf(stdout, "  OK: extension %s (%s)\n", resource.Name, resource.Path)
		}
		for _, resource := range output.Skills {
			_, _ = fmt.Fprintf(stdout, "  OK: skill %s (%s)\n", resource.Name, resource.Path)
		}
		if environment := output.AgentEnvironment; environment != nil {
			_, _ = fmt.Fprintf(stdout, "  OK: agent environment %s (%s), Pig runtime %s@%s, policy %s\n", environment.Form, environment.Value, environment.RuntimeMode, environment.RuntimeVersion, environment.PolicyPreset)
		}
		_, _ = fmt.Fprintf(stdout, "OK: %s (%s): %d extension(s), %d skill(s)\n", output.Name, output.Source, len(output.Extensions), len(output.Skills))
	default:
		for _, message := range output.Errors {
			_, _ = fmt.Fprintf(stderr, "FAIL: %s\n", message)
		}
		_, _ = fmt.Fprintf(stderr, "FAIL: %d validation error(s)\n", len(output.Errors))
	}
	if !output.Valid {
		return 1
	}
	return 0
}

func remotePigletAddOffline() bool {
	if strings.TrimSpace(os.Getenv("PI_OFFLINE")) != "" {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(os.Getenv("PIG_OFFLINE"))) {
	case "1", "true", "yes":
		return true
	default:
		return false
	}
}

type resolvedPigletAddSource struct {
	path             string
	originSource     string
	materializedRoot string
}

func resolvePigletAddSource(raw string, stdout, stderr io.Writer) (resolvedPigletAddSource, error) {
	expanded := expandPath(raw)
	if _, err := os.Stat(expanded); err == nil {
		return resolvedPigletAddSource{path: expanded}, nil
	}
	ref, parseErr := sourceref.Parse(raw, sourceref.Options{Bare: sourceref.BareReject, AllowContributed: true})
	if parseErr != nil {
		return resolvedPigletAddSource{path: expanded}, nil
	}
	if ref.Kind == sourceref.KindLocal {
		return resolvedPigletAddSource{path: expandPath(ref.Locator)}, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return resolvedPigletAddSource{}, err
	}
	// pig additive (D18): Stock Pig materializes npm and Git Piglet source
	// packages without activating them as Pi Packages.
	if ref.Kind == sourceref.KindNPM || ref.Kind == sourceref.KindGit {
		if err := validateRemotePigletOriginSource(ref); err != nil {
			return resolvedPigletAddSource{}, err
		}
		if remotePigletAddOffline() {
			return resolvedPigletAddSource{}, fmt.Errorf("cannot add remote Piglet %q while offline; unset PIG_OFFLINE and PI_OFFLINE to allow fetching", raw)
		}
		materializedRoot, err := installresolver.Materialize(cwd, raw, "user", stdout, stderr)
		if err != nil {
			return resolvedPigletAddSource{}, err
		}
		path, err := findMaterializedPiglet(materializedRoot)
		if err != nil {
			return resolvedPigletAddSource{}, err
		}
		return resolvedPigletAddSource{path: path, originSource: raw, materializedRoot: materializedRoot}, nil
	}
	if ref.Kind != sourceref.KindContributed {
		return resolvedPigletAddSource{}, fmt.Errorf("Piglet source %q must be a local path, npm or Git source, or contributed Piglet source", raw)
	}
	// pig additive (D18): contributed catalogs acquire independent Piglet
	// source without routing it through Package installation.
	path, err := installresolver.ResolvePigletSource(cwd, raw)
	return resolvedPigletAddSource{path: path}, err
}

func findMaterializedPiglet(root string) (string, error) {
	if root == "" {
		return "", fmt.Errorf("materialized Piglet source root is empty")
	}
	relative := "piglet.yaml"
	manifestPath := filepath.Join(root, "package.json")
	if data, err := os.ReadFile(manifestPath); err == nil {
		var manifest struct {
			Pig struct {
				Piglet string `json:"piglet"`
			} `json:"pig"`
		}
		if err := json.Unmarshal(data, &manifest); err != nil {
			return "", fmt.Errorf("read Piglet package manifest %s: %w", manifestPath, err)
		}
		if manifest.Pig.Piglet != "" {
			relative = manifest.Pig.Piglet
		}
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("read Piglet package manifest %s: %w", manifestPath, err)
	}
	if filepath.IsAbs(relative) || strings.Contains(relative, `\`) {
		return "", fmt.Errorf("package.json pig.piglet %q must be a portable relative path", relative)
	}
	clean := filepath.Clean(filepath.FromSlash(relative))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("package.json pig.piglet %q must stay inside the package", relative)
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("resolve materialized Piglet root %s: %w", root, err)
	}
	path := filepath.Join(root, clean)
	realPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("resolve materialized Piglet %s: %w", path, err)
	}
	inside, err := filepath.Rel(realRoot, realPath)
	if err != nil || inside == ".." || strings.HasPrefix(inside, ".."+string(filepath.Separator)) || filepath.IsAbs(inside) {
		return "", fmt.Errorf("materialized Piglet %s resolves outside package root %s", path, root)
	}
	info, err := os.Stat(realPath)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("materialized Piglet %s is not a regular file", path)
	}
	return realPath, nil
}

func isCatalogPigletSource(path string) bool {
	root := codingagent.CatalogRoot(codingagent.AgentDir())
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func validatePortableCatalogPiglet(p *Piglet) error {
	for _, secret := range p.Secrets {
		if secret.From.File != "" {
			return fmt.Errorf("catalog Piglet secret %q uses a machine-local file source", secret.Name)
		}
	}
	if p.AgentEnv != nil {
		if p.AgentEnv.DevContainer != "" {
			return fmt.Errorf("catalog Piglet agentEnv.devContainer requires a published dependency closure")
		}
		if p.AgentEnv.Source != "" {
			ref, err := validateTypedSource(p.AgentEnv.Source, sourceref.BareNPM)
			if err != nil {
				return err
			}
			if ref.Kind == sourceref.KindLocal {
				return fmt.Errorf("catalog Piglet agentEnv.source uses local source %q", p.AgentEnv.Source)
			}
		}
	}
	for _, alias := range slices.Sorted(maps.Keys(p.Packages)) {
		source := p.Packages[alias]
		if ref, err := validateTypedSource(source, sourceref.BareReject); err != nil {
			return err
		} else if ref.Kind == sourceref.KindLocal {
			return fmt.Errorf("catalog Piglet package %q uses local source %q", alias, source)
		}
	}
	for _, extension := range p.Extensions {
		for _, origin := range extension.Origins {
			if err := validatePortableOrigin("extension", extension.Name, origin); err != nil {
				return err
			}
		}
	}
	for _, skill := range p.Skills {
		for _, origin := range skill.Origins {
			if err := validatePortableOrigin("skill", skill.Name, origin); err != nil {
				return err
			}
		}
	}
	return nil
}

func validatePortableOrigin(kind, name, origin string) error {
	if strings.HasPrefix(origin, "package:") {
		return nil
	}
	ref, err := validateTypedSource(origin, sourceref.BareReject)
	if err != nil {
		return err
	}
	if ref.Kind == sourceref.KindLocal {
		return fmt.Errorf("catalog Piglet %s %q uses local source %q", kind, name, origin)
	}
	return nil
}

type pigletAddCandidate struct {
	piglet         *Piglet
	origin         *pigletOrigin
	extensionCount int
	skillCount     int
	files          map[string][]byte
}

type pigletCommandOutput struct {
	Command string `json:"command"`
	Success bool   `json:"success"`
	Output  string `json:"output,omitempty"`
	Error   string `json:"error,omitempty"`
}

func cmdAdd(args []string, stdout, stderr io.Writer) int {
	jsonMode := slices.Contains(args, "--json")
	var filtered []string
	help := false
	var parseErr error
	for _, arg := range args {
		switch arg {
		case "--json", "--no-input":
		case "-h", "--help":
			help = true
		default:
			if strings.HasPrefix(arg, "-") {
				parseErr = fmt.Errorf("unknown option %q", arg)
				continue
			}
			filtered = append(filtered, arg)
		}
	}
	if parseErr != nil {
		if jsonMode {
			writePigletCommandJSON(pigletCommandOutput{Command: "add", Success: false, Error: parseErr.Error()}, stdout)
		} else {
			_, _ = fmt.Fprintf(stderr, "error: %v\n", parseErr)
		}
		return 2
	}
	if help {
		_, _ = fmt.Fprintln(stdout, "Usage: pig piglet add <path|npm:ref|git:ref|catalog-ref> [--json] [--no-input]")
		return 0
	}
	if !jsonMode {
		return cmdAddHuman(filtered, stdout, stderr)
	}
	var humanOut, humanErr strings.Builder
	code := cmdAddHuman(filtered, &humanOut, &humanErr)
	writePigletCommandJSON(pigletCommandOutput{Command: "add", Success: code == 0, Output: strings.TrimSpace(humanOut.String()), Error: strings.TrimSpace(humanErr.String())}, stdout)
	return code
}

func cmdAddHuman(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		_, _ = fmt.Fprintln(stderr, "Usage: pig piglet add <path|npm:ref|git:ref|catalog-ref>")
		return 2
	}
	source, err := resolvePigletAddSource(args[0], stdout, stderr)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	pigletsDir := codingagent.PigletsDir()
	candidate, err := planPigletAdd(source, pigletsDir)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	if candidate.origin != nil {
		destination := filepath.Join(pigletsDir, candidate.piglet.Name+".yaml")
		if _, err := os.Stat(destination); err == nil {
			_, _ = fmt.Fprintf(stderr, "error: Piglet %q is already installed; run `pig piglet update %s` to refresh its origin\n", candidate.piglet.Name, candidate.piglet.Name)
			return 1
		} else if !os.IsNotExist(err) {
			_, _ = fmt.Fprintf(stderr, "error: inspect destination %s: %v\n", destination, err)
			return 1
		}
	}
	if err := commitPigletAddFiles(candidate.files); err != nil {
		_, _ = fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	destination := filepath.Join(pigletsDir, candidate.piglet.Name+".yaml")
	_, _ = fmt.Fprintf(stdout, "Added %s (%d extensions, %d skills) → %s\n",
		candidate.piglet.Name, candidate.extensionCount, candidate.skillCount, destination)
	return 0
}

func planPigletAdd(source resolvedPigletAddSource, pigletsDir string) (pigletAddCandidate, error) {
	p, err := Parse(source.path)
	if err != nil {
		return pigletAddCandidate{}, err
	}
	if p.Extends != nil {
		return pigletAddCandidate{}, fmt.Errorf("Piglet add cannot copy an extends dependency closure; run the Piglet from its source path")
	}
	if p.AgentEnv != nil && p.AgentEnv.DevContainer != "" {
		return pigletAddCandidate{}, fmt.Errorf("Piglet add cannot copy an agentEnv.devContainer closure; run the Piglet from its source path")
	}
	if source.originSource != "" || isCatalogPigletSource(source.path) {
		if err := validatePortableCatalogPiglet(p); err != nil {
			return pigletAddCandidate{}, err
		}
	}
	if err := validatePigletAddOrigins(p); err != nil {
		return pigletAddCandidate{}, err
	}
	resolvedExtensions, extensionErrors := ResolveExtensions(p)
	resolvedSkills, skillErrors := ResolveSkills(p)
	if len(extensionErrors) > 0 || len(skillErrors) > 0 {
		all := append([]error(nil), extensionErrors...)
		all = append(all, skillErrors...)
		return pigletAddCandidate{}, errors.Join(all...)
	}
	pigletData, err := os.ReadFile(source.path)
	if err != nil {
		return pigletAddCandidate{}, err
	}
	files := map[string][]byte{filepath.Join(pigletsDir, p.Name+".yaml"): pigletData}
	var origin *pigletOrigin
	if source.originSource != "" {
		record, err := newPigletOrigin(source.originSource, source.materializedRoot, pigletData, time.Now())
		if err != nil {
			return pigletAddCandidate{}, err
		}
		originData, err := marshalPigletOrigin(record)
		if err != nil {
			return pigletAddCandidate{}, err
		}
		origin = &record
		files[filepath.Join(pigletsDir, p.Name+".origin.json")] = originData
	}
	refs := make([]PromptRef, 0, 1)
	if p.SystemPrompt != nil {
		refs = append(refs, *p.SystemPrompt)
	}
	for i, ref := range refs {
		if ref.File == "" {
			continue
		}
		relative, data, err := readPigletRelativeFile(source.path, ref.File)
		if err != nil {
			return pigletAddCandidate{}, fmt.Errorf("prompt file %d %q: %w", i, ref.File, err)
		}
		target := filepath.Join(pigletsDir, relative)
		if previous, exists := files[target]; exists && !bytes.Equal(previous, data) {
			return pigletAddCandidate{}, fmt.Errorf("prompt file %q conflicts with destination %s", ref.File, target)
		}
		files[target] = data
	}
	return pigletAddCandidate{
		piglet: p, origin: origin, extensionCount: len(resolvedExtensions), skillCount: len(resolvedSkills), files: files,
	}, nil
}

func validatePigletAddOrigins(p *Piglet) error {
	var failures []string
	check := func(kind, name string, origins []string) {
		for _, origin := range origins {
			if strings.HasPrefix(origin, "package:") {
				continue
			}
			ref, err := validateTypedSource(origin, sourceref.BareReject)
			if err != nil || ref.Kind != sourceref.KindLocal || filepath.IsAbs(ref.Locator) {
				continue
			}
			failures = append(failures, fmt.Sprintf("  FAIL: %s %s (%s)", kind, name, origin))
		}
	}
	for _, ext := range p.Extensions {
		check("extension", ext.Name, ext.Origins)
	}
	for _, skill := range p.Skills {
		check("skill", skill.Name, skill.Origins)
	}
	if len(failures) == 0 {
		return nil
	}
	return fmt.Errorf("Piglet add cannot copy relative local origins:\n%s\nRun the Piglet from its source path or use a portable origin", strings.Join(failures, "\n"))
}

func readPigletRelativeFile(pigletPath, raw string) (string, []byte, error) {
	if filepath.IsAbs(raw) {
		return "", nil, fmt.Errorf("must be relative to the Piglet source; absolute paths cannot be copied")
	}
	relative := filepath.Clean(filepath.FromSlash(raw))
	if relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", nil, fmt.Errorf("escapes the Piglet source directory")
	}
	root := filepath.Dir(pigletPath)
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", nil, fmt.Errorf("resolve Piglet source directory: %w", err)
	}
	path := filepath.Join(root, relative)
	realPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", nil, err
	}
	realRelative, err := filepath.Rel(realRoot, realPath)
	if err != nil || realRelative == ".." || strings.HasPrefix(realRelative, ".."+string(filepath.Separator)) {
		return "", nil, fmt.Errorf("resolves outside the Piglet source directory")
	}
	data, err := os.ReadFile(realPath)
	if err != nil {
		return "", nil, err
	}
	return relative, data, nil
}

func writePigletCommandJSON(output pigletCommandOutput, stdout io.Writer) {
	data, err := json.Marshal(output)
	if err != nil {
		_, _ = fmt.Fprintf(stdout, "{\"version\":1,\"success\":false,\"error\":%q}\n", err.Error())
		return
	}
	_, _ = fmt.Fprintln(stdout, string(data))
}

func commitPigletAddFiles(files map[string][]byte) error {
	targets := slices.Sorted(maps.Keys(files))
	for _, target := range targets {
		if existing, err := os.ReadFile(target); err == nil {
			if !bytes.Equal(existing, files[target]) {
				return fmt.Errorf("destination %s already exists with different content; remove it explicitly before replacing", target)
			}
			delete(files, target)
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("inspect destination %s: %w", target, err)
		}
	}
	stages := make(map[string]string, len(files))
	for _, target := range slices.Sorted(maps.Keys(files)) {
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			removePigletInstallStages(stages)
			return err
		}
		stage, err := os.CreateTemp(filepath.Dir(target), ".piglet-install-*.stage")
		if err != nil {
			removePigletInstallStages(stages)
			return err
		}
		stagePath := stage.Name()
		stages[target] = stagePath
		if _, err := stage.Write(files[target]); err != nil {
			_ = stage.Close()
			removePigletInstallStages(stages)
			return err
		}
		if err := stage.Chmod(0o644); err != nil {
			_ = stage.Close()
			removePigletInstallStages(stages)
			return err
		}
		if err := stage.Close(); err != nil {
			removePigletInstallStages(stages)
			return err
		}
	}
	created := make([]string, 0, len(stages))
	for _, target := range slices.Sorted(maps.Keys(stages)) {
		if err := os.Link(stages[target], target); err != nil {
			for _, path := range created {
				_ = os.Remove(path)
			}
			removePigletInstallStages(stages)
			return fmt.Errorf("commit %s: %w", target, err)
		}
		created = append(created, target)
	}
	removePigletInstallStages(stages)
	return nil
}

func removePigletInstallStages(stages map[string]string) {
	for _, stage := range stages {
		_ = os.Remove(stage)
	}
}

// cmdRemove deletes managed Piglet facets. It only touches user Piglet source
// and managed artifact/record stores.
func cmdRemove(args []string, stdout, stderr io.Writer) int {
	jsonMode := false
	filtered := make([]string, 0, len(args))
	for _, arg := range args {
		switch arg {
		case "--json":
			jsonMode = true
		case "--no-input":
		default:
			filtered = append(filtered, arg)
		}
	}
	if !jsonMode {
		return cmdRemoveHuman(filtered, stdout, stderr)
	}
	var humanOut, humanErr strings.Builder
	code := cmdRemoveHuman(filtered, &humanOut, &humanErr)
	writePigletCommandJSON(pigletCommandOutput{Command: "remove", Success: code == 0, Output: strings.TrimSpace(humanOut.String()), Error: strings.TrimSpace(humanErr.String())}, stdout)
	return code
}

func cmdRemoveHuman(args []string, stdout, stderr io.Writer) int {
	name := ""
	removeSource := false
	removeBinary := false
	removeAll := false
	usage := "Usage: pig piglet remove <name> [--source|--binary|--all]"
	for _, arg := range args {
		switch arg {
		case "--source":
			removeSource = true
		case "--binary":
			removeBinary = true
		case "--all":
			removeAll = true
			removeSource = true
			removeBinary = true
		case "-h", "--help":
			_, _ = fmt.Fprintln(stdout, usage)
			return 0
		default:
			if strings.HasPrefix(arg, "-") || name != "" {
				_, _ = fmt.Fprintln(stderr, usage)
				return 2
			}
			name = strings.TrimSuffix(strings.TrimSuffix(arg, ".yaml"), ".yml")
		}
	}
	if name == "" {
		_, _ = fmt.Fprintln(stderr, usage)
		return 2
	}
	piglets, err := List()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "error: Piglet inventory is invalid: %v\n", err)
		return 1
	}
	var sourceInfo *PigletInfo
	var records []RecordInfo
	for i := range piglets {
		if piglets[i].Name != name {
			continue
		}
		records = append(records, piglets[i].Records...)
		if piglets[i].Location == "user" {
			sourceInfo = &piglets[i]
		}
	}
	if sourceInfo == nil && len(records) == 0 {
		_, _ = fmt.Fprintf(stderr, "error: no managed Piglet %q was found\n", name)
		return 1
	}
	if !removeSource && !removeBinary {
		hasSource := sourceInfo != nil
		hasBinary := len(records) > 0
		if hasSource && hasBinary {
			_, _ = fmt.Fprintf(stderr, "error: Piglet %q has source and Binary facets; choose --source, --binary, or --all\n", name)
			return 2
		}
		removeSource = hasSource
		removeBinary = hasBinary
	}
	if removeSource {
		if sourceInfo == nil {
			_, _ = fmt.Fprintf(stderr, "error: Piglet %q has no user source facet\n", name)
			return 1
		}
		userRoot := codingagent.PigletsDir()
		relative, err := filepath.Rel(userRoot, sourceInfo.Path)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			_, _ = fmt.Fprintf(stderr, "error: source facet %s is not user-owned; remove it at its source\n", sourceInfo.Path)
			return 1
		}
	}
	if removeBinary && len(records) == 0 && !removeAll {
		_, _ = fmt.Fprintf(stderr, "error: Piglet %q has no managed Binary facet\n", name)
		return 1
	}
	removals := make([]pigletFacetRemoval, 0, 1+len(records)*2)
	if removeSource {
		removals = append(removals, pigletFacetRemoval{path: sourceInfo.Path, label: "source"})
		originPath := originPathForPiglet(sourceInfo.Path)
		if _, err := os.Stat(originPath); err == nil {
			removals = append(removals, pigletFacetRemoval{path: originPath, label: "origin record"})
		} else if !os.IsNotExist(err) {
			_, _ = fmt.Fprintf(stderr, "error: inspect Piglet origin %s: %v\n", originPath, err)
			return 1
		}
	}
	if removeBinary {
		artifacts := make(map[string]struct{}, len(records))
		resolutions := make(map[string]struct{}, len(records))
		currents := make(map[string]struct{}, len(records))
		for _, record := range records {
			removals = append(removals, pigletFacetRemoval{path: record.Path, label: "Piglet Binary record"})
			if record.ResolutionPath != "" {
				if _, exists := resolutions[record.ResolutionPath]; !exists {
					resolutions[record.ResolutionPath] = struct{}{}
					removals = append(removals, pigletFacetRemoval{path: record.ResolutionPath, label: "Piglet resolution record"})
				}
			}
			if record.CurrentPath != "" {
				if _, exists := currents[record.CurrentPath]; !exists {
					currents[record.CurrentPath] = struct{}{}
					removals = append(removals, pigletFacetRemoval{path: record.CurrentPath, label: "Piglet Binary current pointer"})
				}
			}
			if _, exists := artifacts[record.ArtifactPath]; exists {
				continue
			}
			artifacts[record.ArtifactPath] = struct{}{}
			removals = append(removals, pigletFacetRemoval{path: record.ArtifactPath, label: "managed Piglet Binary"})
		}
	}
	if err := removePigletFacets(removals); err != nil {
		_, _ = fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	for _, removal := range removals {
		_, _ = fmt.Fprintf(stdout, "removed %s: %s\n", removal.label, removal.path)
	}
	if removeBinary {
		pruneEmptyRecordDirs(filepath.Join(codingagent.PigletRecordsDir(), name))
		pruneEmptyRecordDirs(filepath.Join(codingagent.PigletArtifactsDir(), name))
		_, _ = fmt.Fprintln(stdout, "note: externally placed Piglet Binaries and adjacent records are unchanged")
	}
	return 0
}

type pigletFacetRemoval struct {
	path  string
	label string
}

func removePigletFacets(removals []pigletFacetRemoval) error {
	staged := make(map[string]string, len(removals))
	rollback := func() error {
		var errs []error
		for original, stage := range staged {
			if err := os.Rename(stage, original); err != nil {
				errs = append(errs, fmt.Errorf("restore %s: %w", original, err))
			}
		}
		return errors.Join(errs...)
	}
	for _, removal := range removals {
		if _, err := os.Stat(removal.path); err != nil {
			return errors.Join(fmt.Errorf("inspect %s %s: %w", removal.label, removal.path, err), rollback())
		}
		stage, err := os.CreateTemp(filepath.Dir(removal.path), ".piglet-remove-*.stage")
		if err != nil {
			return errors.Join(err, rollback())
		}
		stagePath := stage.Name()
		if err := stage.Close(); err != nil {
			_ = os.Remove(stagePath)
			return errors.Join(err, rollback())
		}
		if err := os.Remove(stagePath); err != nil {
			return errors.Join(err, rollback())
		}
		if err := os.Rename(removal.path, stagePath); err != nil {
			return errors.Join(fmt.Errorf("stage removal of %s: %w", removal.path, err), rollback())
		}
		staged[removal.path] = stagePath
	}
	for original, stage := range staged {
		if err := os.Remove(stage); err != nil {
			return fmt.Errorf("finalize removal of %s (staged at %s): %w", original, stage, err)
		}
	}
	return nil
}

func pruneEmptyRecordDirs(root string) {
	var dirs []string
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err == nil && entry.IsDir() {
			dirs = append(dirs, path)
		}
		return nil
	})
	slices.Reverse(dirs)
	for _, path := range dirs {
		entries, err := os.ReadDir(path)
		if err == nil && len(entries) == 0 {
			_ = os.Remove(path)
		}
	}
}

// ── In-session extension ─────────────────────────────────────────────────────

// BuildExtensionWithPiglet returns an extension.Extension that applies
// Piglet-based tool scoping at runtime. The caller supplies the Piglet selected
// before extension loading. The extension applies tool scope on session_start
// and before_agent_start events. If the Piglet declares a systemPrompt, the
// extension returns it from before_agent_start so Pig core uses it as the
// session system prompt.
//
// The returned Extension registers read-only /piglet inspection. Piglet
// selection/editing is deliberately not available in-session: a different
// composition is selected by a separate Pig invocation.
func BuildExtensionWithPiglet(initial *Piglet) extension.Extension {
	return buildExtension(initial)
}

func buildExtension(initial *Piglet) extension.Extension {
	activePiglet := initial
	var resolvedSystemPrompt string // cached after first resolution

	resolvePiglet := func(flagVal any) {
		if activePiglet != nil {
			return // already resolved
		}

		var nameOrPath string
		if s, ok := flagVal.(string); ok && s != "" {
			nameOrPath = s
		}
		if nameOrPath == "" {
			nameOrPath = os.Getenv("PIG_PIGLET_PATH")
		}
		if nameOrPath == "" {
			nameOrPath = os.Getenv("PIG_PIGLET_NAME")
		}
		if nameOrPath == "" {
			if _, err := os.Stat("/opt/pig/piglet.yaml"); err == nil {
				nameOrPath = "/opt/pig/piglet.yaml"
			}
		}
		if nameOrPath == "" {
			return // no piglet specified
		}

		resolved, err := Resolve(nameOrPath)
		if err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "[piglet] ERROR: %v\n", err)
			os.Exit(1)
		}
		workspace, _ := os.Getwd()
		resolution, err := ResolveEffectiveWithOptions(resolved, ResolveOptions{Workspace: workspace})
		if err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "[piglet] ERROR: %v\n", err)
			os.Exit(1)
		}
		activePiglet = resolution.Piglet
		_, _ = fmt.Fprintf(os.Stderr, "[piglet] Loaded %q from %s\n", activePiglet.Name, resolved)

		// Resolve system prompt if declared.
		if activePiglet.SystemPrompt != nil {
			if activePiglet.SystemPrompt.Text != "" {
				resolvedSystemPrompt = activePiglet.SystemPrompt.Text
			} else if activePiglet.SystemPrompt.File != "" {
				promptPath := expandPath(activePiglet.SystemPrompt.File)
				// Relative paths resolve against piglet directory.
				if !filepath.IsAbs(promptPath) {
					promptPath = filepath.Join(filepath.Dir(resolved), promptPath)
				}
				data, err := os.ReadFile(promptPath)
				if err != nil {
					_, _ = fmt.Fprintf(os.Stderr, "[piglet] warning: systemPrompt file %s: %v\n", promptPath, err)
				} else {
					resolvedSystemPrompt = string(data)
				}
			}
			if resolvedSystemPrompt != "" {
				_, _ = fmt.Fprintf(os.Stderr, "[piglet] System prompt: %d bytes\n", len(resolvedSystemPrompt))
			}
		}
	}

	// Convert extension.ToolInfo (from GetAllTools) to piglet.ToolInfo
	convertTools := func(tools []extension.ToolInfo) []ToolInfo {
		out := make([]ToolInfo, len(tools))
		for i, t := range tools {
			// SourceInfo is currently `any`: try to extract a string source name
			source := ""
			switch s := t.SourceInfo.(type) {
			case string:
				source = s
			case map[string]any:
				if name, ok := s["name"].(string); ok {
					source = name
				}
			}
			if source == "" {
				source = "builtin"
			}
			out[i] = ToolInfo{Name: t.Name, Source: source}
		}
		return out
	}

	applyScoping := func(ctx *extension.Context, reason string) {
		if activePiglet == nil {
			return
		}

		allTools := ctx.GetAllTools()
		if len(allTools) == 0 {
			_, _ = fmt.Fprintln(os.Stderr, "[piglet] No tools registered yet: skipping scoping")
			return
		}

		converted := convertTools(allTools)
		pigletScoped := ScopeTools(activePiglet, converted)

		// Intersect with the current active set so earlier extensions'
		// SetActiveTools calls (e.g. role-based narrowing) are respected.
		// If no prior scoping occurred, currentActive == allTools and the
		// intersection equals pigletScoped.
		currentActive := ctx.GetActiveTools()
		var active []string
		if len(currentActive) > 0 && len(currentActive) < len(allTools) {
			// Another extension already narrowed: intersect.
			allowed := make(map[string]struct{}, len(currentActive))
			for _, name := range currentActive {
				allowed[name] = struct{}{}
			}
			for _, name := range pigletScoped {
				if _, ok := allowed[name]; ok {
					active = append(active, name)
				}
			}
		} else {
			active = pigletScoped
		}

		removed := len(allTools) - len(active)
		if removed > 0 {
			_, _ = fmt.Fprintf(os.Stderr, "[piglet] Applied scoping (%s): %d/%d tools active, %d hidden\n",
				reason, len(active), len(allTools), removed)
		} else {
			_, _ = fmt.Fprintf(os.Stderr, "[piglet] All %d tools active (no scoping restrictions)\n", len(allTools))
		}

		ctx.SetActiveTools(active)
	}

	// Build extension.Extension with handlers and commands
	ext := extension.Extension{
		Name:             "piglet",
		Path:             "builtin:piglet",
		ResolvedPath:     "builtin:piglet",
		Handlers:         map[string][]extension.HandlerFn{},
		Tools:            map[string]extension.RegisteredTool{},
		Commands:         map[string]extension.RegisteredCommand{},
		Flags:            map[string]extension.ExtensionFlag{},
		Shortcuts:        map[extension.KeyID]extension.ExtensionShortcut{},
		MessageRenderers: map[string]extension.MessageRenderer{},
	}

	// Register --piglet flag
	ext.Flags["piglet"] = extension.ExtensionFlag{
		Name:          "piglet",
		Description:   "Path to piglet YAML or piglet name",
		Type:          extension.FlagString,
		ExtensionPath: "builtin:piglet",
	}

	// session_start handler: resolve + apply
	ext.Handlers["session_start"] = []extension.HandlerFn{
		func(args ...any) (any, error) {
			if len(args) < 2 {
				return nil, nil
			}
			ctx, _ := args[1].(context.Context)
			if ctx == nil {
				return nil, nil
			}
			extCtx := extension.FromContext(ctx)
			if extCtx == nil {
				return nil, nil
			}
			flagVal := extCtx.GetFlagValue("piglet")
			resolvePiglet(flagVal)
			applyScoping(extCtx, "session_start")
			return nil, nil
		},
	}

	// before_agent_start handler: reapply in case extensions registered new tools
	ext.Handlers["before_agent_start"] = []extension.HandlerFn{
		func(args ...any) (any, error) {
			if len(args) < 2 {
				return nil, nil
			}
			ctx, _ := args[1].(context.Context)
			if ctx == nil {
				return nil, nil
			}
			extCtx := extension.FromContext(ctx)
			if extCtx == nil {
				return nil, nil
			}
			flagVal := extCtx.GetFlagValue("piglet")
			resolvePiglet(flagVal)
			applyScoping(extCtx, "before_agent_start")

			// Return system prompt override if the piglet declares one.
			if resolvedSystemPrompt != "" {
				return &extension.BeforeAgentStartEventResult{
					SystemPrompt: new(resolvedSystemPrompt),
				}, nil
			}
			return nil, nil
		},
	}

	// /piglet is read-only inspection of the composition active in this process.
	ext.Commands["piglet"] = extension.RegisteredCommand{
		Name:        "piglet",
		Description: "Inspect the active Piglet composition",
		Handler: func(ctx context.Context, args string) error {
			extCtx := extension.FromContext(ctx)
			notify := func(message string) {
				if extCtx != nil {
					if ui, err := extCtx.UI(); err == nil {
						ui.Notify(message, "info")
						return
					}
				}
				_, _ = fmt.Fprintln(os.Stderr, message)
			}
			if strings.TrimSpace(args) != "" {
				notify("/piglet is read-only; run `pig --piglet <name|path>` in a separate invocation")
				return nil
			}
			notify(formatActivePiglet(activePiglet))
			return nil
		},
	}

	return ext
}

func formatActivePiglet(active *Piglet) string {
	if active == nil {
		return "Piglet: none"
	}
	var out strings.Builder
	_, _ = fmt.Fprintf(&out, "Piglet: %s", active.Name)
	if active.Release != nil && active.Release.Version != "" {
		_, _ = fmt.Fprintf(&out, "\nRelease: %s", active.Release.Version)
	}
	if source := active.SourcePath(); source != "" {
		_, _ = fmt.Fprintf(&out, "\nSource: %s", source)
	}
	return out.String()
}
