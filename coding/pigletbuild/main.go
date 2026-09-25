// Piglet Binary build implementation. The public entry point is only
// `pig piglet build`; no standalone build namespace is exposed.
package pigletbuild

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"gopkg.in/yaml.v3"

	piglet "github.com/MichaelKinsy/PiG/coding/piglet"
	"github.com/MichaelKinsy/PiG/coding/piglet/signature"
	"github.com/MichaelKinsy/PiG/internal/buildprogress"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// RunPigletBuildCommand handles the sole public Piglet output build path.
func RunPigletBuildCommand(args []string, stdout, stderr io.Writer) int {
	return runBuild(args, stdout, stderr)
}

type pigletBuildOutput struct {
	Success     bool               `json:"success"`
	Piglet      string             `json:"piglet,omitempty"`
	Format      string             `json:"format,omitempty"`
	Builder     string             `json:"builder,omitempty"`
	Output      string             `json:"output,omitempty"`
	Artifact    string             `json:"artifact,omitempty"`
	Record      string             `json:"record,omitempty"`
	Log         string             `json:"log,omitempty"`
	Error       string             `json:"error,omitempty"`
	Unavailable []BuilderReadiness `json:"unavailable,omitempty"`
}

const buildUsage = `Usage:
  pig piglet build <name> --format script --out <path|->
  pig piglet build <name> --format binary --out <path>
  pig piglet build <name> --format image --out <reference>

Options:
  --format <script|binary|image>  Select one output format; image is reserved and currently fails
  --out <path|->                 Write the selected output; script accepts - for stdout
  --targets <os/arch,...>        Select Binary build targets
  --builder <auto|native|name>   Select a registered Binary builder
  --verification <basic>         Select the registered verification policy
  --workspace <path>             Resolve workspace-bound Piglet inputs from this path
  --locked                       Reserved for Binary and Image artifact locking; currently fails
  --record <path>                Reserved for an explicit artifact record; currently fails
  --verbose                      Stream toolchain output prefixed by member
  --json                         Emit structured output
  --no-input                     Do not prompt
  -h, --help                     Show this help
`

func runBuild(args []string, stdout, stderr io.Writer) int {
	for _, arg := range args {
		if arg == "-h" || arg == "--help" {
			_, _ = io.WriteString(stdout, buildUsage)
			return 0
		}
	}
	jsonMode := hasBuildFlag(args, "--json")
	pigletName, opts, _, outPath, err := parseArgs(args)
	if err != nil {
		return renderBuildError(jsonMode, pigletName, err, 2, stdout, stderr)
	}
	if pigletName == "" {
		return renderBuildError(jsonMode, "", fmt.Errorf("piglet is required"), 2, stdout, stderr)
	}
	if opts.Format == "script" {
		p, err := loadScriptPiglet(pigletName, opts.Workspace)
		if err != nil {
			return renderBuildError(jsonMode, pigletName, err, 1, stdout, stderr)
		}
		return runScriptBuild(args, p, outPath, jsonMode, stdout, stderr)
	}
	// pig additive (D18): explicit artifact builds select product-neutral phase diagnostics.
	progress := buildprogress.New(stderr, hasBuildFlag(args, "--verbose"))
	defer progress.Close()
	ctx := buildprogress.Observe(context.Background(), progress.Handle, hasBuildFlag(args, "--verbose"))
	buildprogress.Phase(ctx, "Resolving manifest", pigletName)
	return runBinaryBuild(ctx, progress, pigletName, opts, args, outPath, jsonMode, stdout, stderr)
}

func runBinaryBuild(ctx context.Context, progress *buildprogress.Reporter, pigletName string, opts Options, args []string, outPath string, jsonMode bool, stdout, stderr io.Writer) int {
	fail := func(name string, err error) int {
		return renderBuildError(jsonMode, name, progress.Failure(err), 1, stdout, stderr)
	}
	p, err := loadPiglet(pigletName, opts.Workspace)
	if err != nil {
		return fail(pigletName, err)
	}
	if opts.Format == "image" {
		return fail(p.Name, fmt.Errorf("Piglet Image build is not implemented"))
	}
	if err := p.ValidateAgentEnvironmentRuntime(); err != nil {
		return fail(p.Name, err)
	}
	if err := applyPigletBuildDefaults(p, args, &opts, &outPath); err != nil {
		return fail(p.Name, err)
	}
	if opts.SignKeyPath != "" {
		if opts.SignKey, err = signature.ReadPrivateKey(opts.SignKeyPath); err != nil {
			return fail(p.Name, fmt.Errorf("--sign-key: %w", err))
		}
	}

	buildprogress.Phase(ctx, "Resolving extensions", p.Name)
	cells, warnings := resolvePigletCells(p)
	exts := extensionInputsFromCells(cells)
	requireFused := p.Build != nil && p.Build.ExtensionRealization == "fused"
	verdict := Validate(BuildPlan(exts, opts), warnings, len(exts), requireFused)
	if !verdict.OK {
		return fail(p.Name, fmt.Errorf("Piglet will not build: %s", strings.Join(verdict.Blockers, "; ")))
	}
	buildprogress.Phase(ctx, "Packing resources", "Inlining prompts and skills")
	if err := applyBakedPiglet(pigletName, p, &opts); err != nil {
		return fail(p.Name, err)
	}
	buildStdout := stdout
	var buildLog strings.Builder
	if jsonMode {
		buildStdout = &buildLog
	}
	buildprogress.Phase(ctx, "Selecting builder", opts.Builder)
	builders, err := configuredBuilders()
	if err != nil {
		return fail(p.Name, err)
	}
	result, err := executeBuild(ctx, builders, opts.Builder, BuilderRequest{
		Piglet: p, Cells: cells, Options: opts, Output: outPath, Stdout: buildStdout, Stderr: progress,
	})
	if err != nil {
		return fail(p.Name, err)
	}
	info, err := os.Stat(result.Artifact)
	if err != nil {
		return fail(p.Name, err)
	}
	progress.Success(result.Artifact, info.Size())
	if jsonMode {
		writeBuildJSON(pigletBuildOutput{
			Success: true, Piglet: p.Name, Format: "binary", Builder: result.Builder, Artifact: result.Artifact,
			Record: result.Record, Log: strings.TrimSpace(buildLog.String()),
		}, stdout)
	}
	return 0
}

func renderBuildError(jsonMode bool, pigletName string, err error, code int, stdout, stderr io.Writer) int {
	if jsonMode {
		output := pigletBuildOutput{Success: false, Piglet: pigletName, Error: err.Error()}
		var unavailable *BuilderUnavailableError
		var noneReady *NoBuilderReadyError
		switch {
		case errors.As(err, &unavailable):
			output.Unavailable = []BuilderReadiness{unavailable.Readiness}
		case errors.As(err, &noneReady):
			output.Unavailable = noneReady.Readiness
		}
		writeBuildJSON(output, stdout)
	} else {
		_, _ = fmt.Fprintf(stderr, "pig piglet build: %v\n", err)
		var unavailable *BuilderUnavailableError
		var noneReady *NoBuilderReadyError
		if errors.As(err, &unavailable) || errors.As(err, &noneReady) {
			_, _ = fmt.Fprintln(stderr, "Run `pig setup` to see which build toolchains are missing and how to install them.")
		}
	}
	return code
}

func writeBuildJSON(output pigletBuildOutput, stdout io.Writer) {
	data, err := json.Marshal(output)
	if err != nil {
		_, _ = fmt.Fprintf(stdout, "{\"version\":1,\"success\":false,\"error\":%q}\n", err.Error())
		return
	}
	_, _ = fmt.Fprintln(stdout, string(data))
}

func loadPiglet(name string, workspaces ...string) (*piglet.Piglet, error) {
	path, err := resolvePigletPath(name)
	if err != nil {
		return nil, fmt.Errorf("piglet %q: %w", name, err)
	}
	authored, err := piglet.Parse(path)
	if err != nil {
		return nil, fmt.Errorf("piglet %q: %w", name, err)
	}
	workspace := ""
	if len(workspaces) > 0 {
		workspace = workspaces[0]
	}
	resolution, err := piglet.ResolveEffectiveWithOptions(path, piglet.ResolveOptions{Workspace: workspace})
	if err != nil {
		return nil, fmt.Errorf("piglet %q: %w", name, err)
	}
	// release/build are child-owned orchestration inputs, deliberately absent
	// from the effective composition (S1 R7). The build loader reattaches only
	// the selected child source's metadata for build planning.
	resolution.Piglet.Release = authored.Release
	resolution.Piglet.Build = authored.Build
	return resolution.Piglet, nil
}

func loadScriptPiglet(name, workspace string) (*piglet.Piglet, error) {
	path, err := resolvePigletPath(name)
	if err != nil {
		return nil, fmt.Errorf("piglet %q: %w", name, err)
	}
	resolution, err := piglet.ResolveEffectiveWithOptions(path, piglet.ResolveOptions{Workspace: workspace})
	if err != nil {
		return nil, fmt.Errorf("piglet %q: %w", name, err)
	}
	if resolution.Piglet.SourcePath() == "" {
		return nil, fmt.Errorf("piglet %q: canonical source path is unavailable", name)
	}
	return resolution.Piglet, nil
}

func runScriptBuild(args []string, p *piglet.Piglet, outPath string, jsonMode bool, stdout, stderr io.Writer) int {
	var artifactFlags []string
	for _, name := range []string{"--targets", "--builder", "--verification", "--locked", "--record", "--sign-key"} {
		if hasBuildFlag(args, name) {
			artifactFlags = append(artifactFlags, name)
		}
	}
	if len(artifactFlags) > 0 {
		return renderBuildError(jsonMode, p.Name, fmt.Errorf("--format script does not accept artifact options: %s", strings.Join(artifactFlags, ", ")), 2, stdout, stderr)
	}
	if outPath == "" {
		return renderBuildError(jsonMode, p.Name, fmt.Errorf("--format script requires explicit --out <path|->"), 2, stdout, stderr)
	}
	if jsonMode && outPath == "-" {
		return renderBuildError(true, p.Name, fmt.Errorf("--format script --out - cannot be combined with --json"), 2, stdout, stderr)
	}
	script := sourceScript(runtime.GOOS, p.SourcePath())
	if outPath == "-" {
		_, _ = stdout.Write(script)
		return 0
	}
	absolute, err := writeSourceScript(outPath, script)
	if err != nil {
		return renderBuildError(jsonMode, p.Name, err, 1, stdout, stderr)
	}
	if jsonMode {
		writeBuildJSON(pigletBuildOutput{Success: true, Piglet: p.Name, Format: "script", Output: absolute}, stdout)
	} else {
		_, _ = fmt.Fprintf(stdout, "Piglet script written: %s\n", absolute)
	}
	return 0
}

func writeSourceScript(path string, data []byte) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if _, err := os.Lstat(absolute); err == nil {
		return "", fmt.Errorf("output %s already exists; choose a different --out path or remove it explicitly", absolute)
	} else if !os.IsNotExist(err) {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(absolute), 0o755); err != nil {
		return "", err
	}
	stage, err := os.CreateTemp(filepath.Dir(absolute), ".pig-piglet-script-*.stage")
	if err != nil {
		return "", err
	}
	stagePath := stage.Name()
	defer func() { _ = os.Remove(stagePath) }()
	if _, err := stage.Write(data); err != nil {
		_ = stage.Close()
		return "", err
	}
	if err := stage.Chmod(0o755); err != nil {
		_ = stage.Close()
		return "", err
	}
	if err := stage.Close(); err != nil {
		return "", err
	}
	if err := os.Link(stagePath, absolute); err != nil {
		return "", fmt.Errorf("commit Piglet script %s: %w", absolute, err)
	}
	return absolute, nil
}

// sourceScript is the launcher `--format script` writes for source: a POSIX
// shell script, or on Windows a cmd.exe batch file with CRLF line endings.
func sourceScript(goos, source string) []byte {
	if goos == "windows" {
		// pig divergence (D69): a Windows Piglet script is a cmd.exe launcher.
		// setlocal turns off delayed expansion that the calling cmd.exe may
		// have enabled, so a ! in the source path stays literal.
		return []byte("@setlocal DisableDelayedExpansion\r\n@pig --piglet " + quoteBatch(source) + " %*\r\n")
	}
	return []byte("#!/bin/sh\nexec pig --piglet " + quotePOSIX(source) + " \"$@\"\n")
}

func quotePOSIX(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

// quoteBatch quotes a Windows path for a batch file. Inside double quotes
// cmd.exe reads & | < > ^ and spaces literally, and a batch file reads %% as
// one %. A Windows path cannot contain a double quote.
func quoteBatch(value string) string {
	return `"` + strings.ReplaceAll(value, "%", "%%") + `"`
}

func resolvePigletPath(name string) (string, error) {
	if _, err := os.Stat(name); err == nil {
		return name, nil
	}
	return piglet.Resolve(name)
}

// parseArgs reads the Piglet Binary build's positional Piglet name and flags.
func parseArgs(args []string) (pigletName string, opts Options, jsonOut bool, outPath string, err error) {
	opts.Builder = "auto"
	opts.Verification = "basic"
	var targetSpec string

	fail := func(e error) (string, Options, bool, string, error) {
		return "", opts, false, "", e
	}
	take := func(i *int, inline string) (string, error) {
		if inline != "" {
			return inline, nil
		}
		*i++
		if *i >= len(args) {
			return "", fmt.Errorf("missing value")
		}
		return args[*i], nil
	}

	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !strings.HasPrefix(arg, "-") {
			if pigletName != "" {
				return fail(fmt.Errorf("unexpected argument %q", arg))
			}
			pigletName = arg
			continue
		}
		name, inline, _ := strings.Cut(arg, "=")
		switch name {
		case "--verbose":
		case "--json":
			jsonOut = true
		case "--no-input":
		case "--format":
			v, e := take(&i, inline)
			if e != nil {
				return fail(fmt.Errorf("--format: %w", e))
			}
			switch v {
			case "script", "binary", "image":
				opts.Format = v
			default:
				return fail(fmt.Errorf("--format must be script, binary, or image, got %q", v))
			}
		case "--locked":
			opts.Locked = true
		case "--record":
			v, e := take(&i, inline)
			if e != nil {
				return fail(fmt.Errorf("--record: %w", e))
			}
			opts.Record = v
		case "--builder":
			v, e := take(&i, inline)
			if e != nil {
				return fail(fmt.Errorf("--builder: %w", e))
			}
			opts.Builder = v
		case "--verification":
			v, e := take(&i, inline)
			if e != nil {
				return fail(fmt.Errorf("--verification: %w", e))
			}
			opts.Verification = v
		case "--out":
			v, e := take(&i, inline)
			if e != nil {
				return fail(fmt.Errorf("--out: %w", e))
			}
			outPath = v
		case "--targets":
			v, e := take(&i, inline)
			if e != nil {
				return fail(fmt.Errorf("--targets: %w", e))
			}
			targetSpec = v
		case "--workspace":
			v, e := take(&i, inline)
			if e != nil {
				return fail(fmt.Errorf("--workspace: %w", e))
			}
			opts.Workspace = v
		case "--sign-key":
			v, e := take(&i, inline)
			if e != nil {
				return fail(fmt.Errorf("--sign-key: %w", e))
			}
			opts.SignKeyPath = v
		default:
			return fail(fmt.Errorf("unknown flag %q", arg))
		}
	}

	if opts.Format == "" {
		return fail(fmt.Errorf("--format is required: script, binary, or image"))
	}
	if opts.Format != "script" && (opts.Locked || opts.Record != "") {
		return fail(fmt.Errorf("--locked and --record Piglet artifact builds are not implemented"))
	}
	if opts.Format != "script" && opts.Verification != "basic" {
		return fail(fmt.Errorf("verification policy %q is not registered; available: basic", opts.Verification))
	}

	native := Target{OS: runtime.GOOS, Arch: runtime.GOARCH}
	opts.Sandbox = Sandbox{Native: native}
	if opts.Workspace == "" {
		opts.Workspace, _ = os.Getwd()
	}
	opts.Targets, err = parseTargets(targetSpec, native)
	if err != nil {
		return fail(err)
	}
	return pigletName, opts, jsonOut, outPath, nil
}

func applyPigletBuildDefaults(p *piglet.Piglet, args []string, opts *Options, outPath *string) error {
	if p.Release != nil {
		opts.Version = p.Release.Version
	}
	if p.Build == nil {
		return nil
	}
	if len(p.Build.Targets) > 0 && !hasBuildFlag(args, "--targets") {
		targets, err := parseTargets(strings.Join(p.Build.Targets, ","), opts.Sandbox.Native)
		if err != nil {
			return fmt.Errorf("piglet build.targets: %w", err)
		}
		opts.Targets = targets
	}
	if outPath != nil && *outPath == "" && p.Build.OutputName != "" && !hasBuildFlag(args, "--out") {
		*outPath = p.Build.OutputName
	}
	return nil
}

func hasBuildFlag(args []string, name string) bool {
	for _, arg := range args {
		if arg == name || strings.HasPrefix(arg, name+"=") {
			return true
		}
	}
	return false
}

func parseTargets(spec string, native Target) ([]Target, error) {
	if spec == "" {
		return []Target{native}, nil
	}
	var targets []Target
	for tok := range strings.SplitSeq(spec, ",") {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		os, arch, ok := strings.Cut(tok, "/")
		if !ok || os == "" || arch == "" {
			return nil, fmt.Errorf("target %q must be os/arch", tok)
		}
		targets = append(targets, Target{OS: os, Arch: arch})
	}
	if len(targets) == 0 {
		return []Target{native}, nil
	}
	return targets, nil
}

// applyBakedPiglet computes the Piglet YAML embedded into the Piglet Binary. The
// baked piglet carries the agent shape (extensions, skills, MCP, scoping,
// discovery) so the Piglet Binary can assemble the intended agent. Prompt files and
// skills are inlined; extension source origins are stripped because the runnable
// binaries come from the embedded cell manifest.
func applyBakedPiglet(pigletName string, p *piglet.Piglet, opts *Options) error {
	pigletDir := ""
	if path, err := resolvePigletPath(pigletName); err == nil {
		pigletDir = filepath.Dir(path)
	}
	baked, err := bakePiglet(p, pigletDir)
	if err != nil {
		return err
	}
	opts.BakedSettings = baked
	return nil
}

func bakePiglet(p *piglet.Piglet, pigletDir string) ([]byte, error) {
	baked := piglet.Clone(p)
	baked.Build = nil
	baked.Release = nil
	if baked.SystemPrompt != nil {
		sp, err := bakePromptRef(*baked.SystemPrompt, pigletDir, "system prompt")
		if err != nil {
			return nil, err
		}
		baked.SystemPrompt = &sp
	}
	if err := bakeSkills(baked); err != nil {
		return nil, err
	}
	baked.Packages = nil
	for i := range baked.Extensions {
		baked.Extensions[i].Origins = nil
	}
	data, err := yaml.Marshal(baked)
	if err != nil {
		return nil, err
	}
	if _, err := piglet.ParseBytes(data); err != nil {
		return nil, err
	}
	return data, nil
}

func bakePromptRef(ref piglet.PromptRef, pigletDir, label string) (piglet.PromptRef, error) {
	if ref.File == "" {
		return ref, nil
	}
	promptPath := ref.File
	if !filepath.IsAbs(promptPath) {
		promptPath = filepath.Join(pigletDir, promptPath)
	}
	data, err := os.ReadFile(promptPath)
	if err != nil {
		return piglet.PromptRef{}, fmt.Errorf("bake %s: %w", label, err)
	}
	return piglet.PromptRef{Text: string(data)}, nil
}

func bakeSkills(p *piglet.Piglet) error {
	resolved, errs := piglet.ResolveSkills(p)
	if len(errs) > 0 {
		return errs[0]
	}
	byName := map[string]string{}
	for _, s := range resolved {
		byName[s.Entry.Name] = s.Path
	}
	for i := range p.Skills {
		if p.Skills[i].Content != "" {
			p.Skills[i].Origins = nil
			continue
		}
		path := byName[p.Skills[i].Name]
		if path == "" {
			return fmt.Errorf("bake skill %q: explicit origin required", p.Skills[i].Name)
		}
		defs, err := codingagent.LoadSkillsFromPath(path)
		if err != nil {
			return fmt.Errorf("bake skill %q: %w", p.Skills[i].Name, err)
		}
		def, err := chooseSkillDef(p.Skills[i].Name, defs)
		if err != nil {
			return err
		}
		p.Skills[i].Name = def.Name
		p.Skills[i].Description = def.Description
		p.Skills[i].Content = def.Body
		p.Skills[i].Origins = nil
	}
	return nil
}

func chooseSkillDef(name string, defs []*codingagent.SkillDef) (*codingagent.SkillDef, error) {
	if len(defs) == 0 {
		return nil, fmt.Errorf("bake skill %q: no SKILL.md found", name)
	}
	if len(defs) == 1 {
		return defs[0], nil
	}
	for _, def := range defs {
		if def.Name == name {
			return def, nil
		}
	}
	return nil, fmt.Errorf("bake skill %q: origin resolved to %d skills", name, len(defs))
}
