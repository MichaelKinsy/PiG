package evals

// Ports packages/evals/src/harness.ts: model selection, process isolation, the documentation variant and the system
// prompt checks. The agent Session runner (RunPiCodingAgent) is in harness_run.go.

import (
	"errors"
	"fmt"
	"math"
	"os"
	"slices"
	"strings"

	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/jsnumber"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// PiCodingAgentModelSelection names the provider and model an eval runs.
type PiCodingAgentModelSelection struct {
	Provider string `json:"provider"`
	ID       string `json:"id"`
}

// PiCodingAgentHarnessOptions configures an eval harness. A nil Tools keeps the Session default. PigPath and
// ExtensionPath name the pig binary and the cmd/pig-eval-extension binary; empty values read PI_EVAL_PIG and
// PI_EVAL_EXTENSION, and the pig binary falls back to PATH.
type PiCodingAgentHarnessOptions struct {
	Name                    string
	Model                   *PiCodingAgentModelSelection
	NoTools                 string
	Tools                   []string
	CustomTools             []CustomTool
	WorkspaceFiles          map[string]string
	TransformSystemPrompt   func(defaultPrompt string) (string, error)
	ExpectedPiDocumentation *bool
	Output                  PiCodingAgentOutput
	PigPath                 string
	ExtensionPath           string

	// agentFiles are written into the isolated agent directory before the model is resolved; in-package tests use them
	// to configure a provider that reaches a local server.
	agentFiles map[string]string
}

func processEnvironment(name string) (string, bool) { return os.LookupEnv(name) }

func environmentLookup(environment []map[string]string) func(string) (string, bool) {
	if len(environment) == 0 {
		return processEnvironment
	}
	return func(name string) (string, bool) {
		value, ok := environment[0][name]
		return value, ok
	}
}

// ResolveModelSelection prefers explicitModel and otherwise reads PI_PROVIDER and PI_MODEL from environment, which
// defaults to the process environment.
func ResolveModelSelection(explicitModel *PiCodingAgentModelSelection, environment ...map[string]string) (PiCodingAgentModelSelection, error) {
	lookup := environmentLookup(environment)
	provider, _ := lookup("PI_PROVIDER")
	id, _ := lookup("PI_MODEL")
	if explicitModel != nil {
		provider, id = explicitModel.Provider, explicitModel.ID
	}
	provider, id = jsstring.Trim(provider), jsstring.Trim(id)
	if provider == "" || id == "" {
		return PiCodingAgentModelSelection{}, errors.New("Select a harness model explicitly or set both PI_PROVIDER and PI_MODEL as defaults.")
	}
	return PiCodingAgentModelSelection{Provider: provider, ID: id}, nil
}

// agentDirEnvironment is the variable that selects the agent directory.
func agentDirEnvironment() string {
	// pig divergence (D2): PiG reads PIG_CODING_AGENT_DIR unless PIG_USE_PI_DIRS selects Pi's PI_CODING_AGENT_DIR.
	if icodingagent.UsePiDirs() {
		return "PI_CODING_AGENT_DIR"
	}
	return icodingagent.ENV_AGENT_DIR
}

// hostAgentDirectory is the agent directory the runner itself reads credentials and model configuration from.
func hostAgentDirectory() string {
	if configured := os.Getenv(agentDirEnvironment()); configured != "" {
		return configured
	}
	return icodingagent.DefaultAgentDir()
}

// ApplyIsolatedEnvironment removes the runner's PI_EVAL_* variables and points HOME, USERPROFILE and the agent
// directory at the isolated paths. The returned function restores every variable it changed.
func ApplyIsolatedEnvironment(home, agentDir string) func() {
	type saved struct {
		name    string
		value   string
		present bool
	}
	var previous []saved
	seen := map[string]bool{}
	for _, entry := range os.Environ() {
		name, value, _ := strings.Cut(entry, "=")
		if !strings.HasPrefix(name, "PI_EVAL_") || seen[name] {
			continue
		}
		seen[name] = true
		previous = append(previous, saved{name, value, true})
		_ = os.Unsetenv(name)
	}
	// PiG's state root variables would send the agent's docs, cache and SDK state outside the isolated home (PIG_HOME, and
	// XDG_CONFIG_HOME through codingagent.ConfigRoot, which CI sets); Pi has no such variables.
	for _, name := range [...]string{"PIG_HOME", "PI_HOME", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME", "XDG_STATE_HOME"} {
		if !seen[name] {
			seen[name] = true
			value, present := os.LookupEnv(name)
			previous = append(previous, saved{name, value, present})
		}
		_ = os.Unsetenv(name)
	}
	for _, override := range [...][2]string{{"HOME", home}, {"USERPROFILE", home}, {agentDirEnvironment(), agentDir}} {
		name := override[0]
		if !seen[name] {
			seen[name] = true
			value, present := os.LookupEnv(name)
			previous = append(previous, saved{name, value, present})
		}
		_ = os.Setenv(name, override[1])
	}
	return func() {
		for _, variable := range previous {
			if variable.present {
				_ = os.Setenv(variable.name, variable.value)
			} else {
				_ = os.Unsetenv(variable.name)
			}
		}
	}
}

type sandboxIdentity struct{ uid, gid int }

// maxSafeInteger is Number.MAX_SAFE_INTEGER.
const maxSafeInteger = 1<<53 - 1

func parseSandboxID(name string) (int, bool, error) {
	value, ok := os.LookupEnv(name)
	if !ok {
		return 0, false, nil
	}
	id := jsnumber.Parse(value)
	if id != math.Trunc(id) || id < 1 || id > maxSafeInteger {
		return 0, false, fmt.Errorf("%s must be a positive integer.", name)
	}
	return int(id), true, nil
}

func resolveSandboxIdentity() (*sandboxIdentity, error) {
	uid, hasUID, err := parseSandboxID("PI_EVAL_SANDBOX_UID")
	if err != nil {
		return nil, err
	}
	gid, hasGID, err := parseSandboxID("PI_EVAL_SANDBOX_GID")
	if err != nil {
		return nil, err
	}
	if !hasUID && !hasGID {
		return nil, nil
	}
	if !hasUID || !hasGID {
		return nil, errors.New("Set both PI_EVAL_SANDBOX_UID and PI_EVAL_SANDBOX_GID, or neither.")
	}
	return &sandboxIdentity{uid: uid, gid: gid}, nil
}

// documentationSectionStart opens the docs section of the default system prompt.
// pig additive (D22): PiG's docs section introduces the materialized PiG documentation bundle.
const documentationSectionStart = "\n<docs>\nPiG documentation (read only"

// VerifySystemPrompt checks that systemPrompt kept its rules and has the documentation section the variant expects.
// It returns systemPrompt unchanged when the options expect nothing.
func VerifySystemPrompt(systemPrompt string, options PiCodingAgentHarnessOptions) (string, error) {
	if options.ExpectedPiDocumentation == nil {
		return systemPrompt, nil
	}
	if !strings.Contains(systemPrompt, "\n<rules>\n") {
		return "", fmt.Errorf("Pi system prompt lost its rules in the %s eval variant.", options.Name)
	}
	if strings.Contains(systemPrompt, documentationSectionStart) != *options.ExpectedPiDocumentation {
		return "", fmt.Errorf("Pi system prompt does not match the %s eval variant.", options.Name)
	}
	return systemPrompt, nil
}

// DocumentationEvalTools are the tools documentation evals run with; they exclude shell and unrestricted network
// tools.
var DocumentationEvalTools = [...]string{"read", "write", "edit", "grep", "find", "ls"}

// ResolveDocumentationVariant validates value, which defaults to PI_EVAL_VARIANT when omitted.
func ResolveDocumentationVariant(value ...string) (DocumentationVariant, error) {
	var variant string
	if len(value) > 0 {
		variant = value[0]
	} else {
		variant, _ = os.LookupEnv("PI_EVAL_VARIANT")
	}
	if variant := DocumentationVariant(variant); variant == DocumentationVariantWithoutDocs || variant == DocumentationVariantWithDocs {
		return variant, nil
	}
	return "", errors.New(`PI_EVAL_VARIANT must be "without_docs" or "with_docs".`)
}

// ExcludePiDocumentation removes the documentation section from the default system prompt. It fails when the
// section or the working-directory section after it is missing.
func ExcludePiDocumentation(defaultPrompt string) (string, error) {
	const documentationStartMarker = "\n<docs>\n"
	const documentationEndMarker = "\n</docs>"
	documentationStart := strings.Index(defaultPrompt, documentationStartMarker)
	if documentationStart == -1 {
		return "", errors.New("Default Pi system prompt has no Pi documentation section.")
	}
	documentationEnd := strings.Index(defaultPrompt[documentationStart:], documentationEndMarker)
	if documentationEnd == -1 {
		return "", errors.New("Default Pi system prompt has no complete Pi documentation section.")
	}
	documentationEnd += documentationStart
	if cwdStart := strings.LastIndex(defaultPrompt, "\n<cwd>\n"); cwdStart < documentationEnd {
		return "", errors.New("Default Pi system prompt has no working-directory section.")
	}
	return defaultPrompt[:documentationStart] + defaultPrompt[documentationEnd+len(documentationEndMarker):], nil
}

// CreatePiDocumentationEvalHarness returns the harness options of the documentation variant named by
// PI_EVAL_VARIANT. It fails outside the isolated container sandbox (PI_EVAL_CONTAINER=1 with a sandbox identity).
// Tools default to DocumentationEvalTools; the without_docs variant strips the documentation section.
func CreatePiDocumentationEvalHarness(options ...PiCodingAgentHarnessOptions) (PiCodingAgentHarnessOptions, error) {
	sandboxed := false
	if container, _ := os.LookupEnv("PI_EVAL_CONTAINER"); container == "1" {
		identity, err := resolveSandboxIdentity()
		if err != nil {
			return PiCodingAgentHarnessOptions{}, err
		}
		sandboxed = identity != nil
	}
	if !sandboxed {
		return PiCodingAgentHarnessOptions{}, errors.New("Documentation evals must run in the isolated container sandbox.")
	}
	variant, err := ResolveDocumentationVariant()
	if err != nil {
		return PiCodingAgentHarnessOptions{}, err
	}
	var harness PiCodingAgentHarnessOptions
	if len(options) > 0 {
		harness = options[0]
	}
	harness.Name = string(variant)
	if harness.Tools == nil {
		harness.Tools = slices.Clone(DocumentationEvalTools[:])
	}
	harness.TransformSystemPrompt = nil
	if variant == DocumentationVariantWithoutDocs {
		harness.TransformSystemPrompt = ExcludePiDocumentation
	}
	harness.ExpectedPiDocumentation = new(variant == DocumentationVariantWithDocs)
	return harness, nil
}
