// Command customfactoryledger derives the internal custom-UI factory draft ledger
// from the pinned upstream public interface inventory.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/coding"
)

const outputName = "custom-factory-call-surface-draft-v" + coding.UpstreamVersion + ".json"

const customFactoryRoot = "pkg:coding-agent/.#ExtensionUIContext::property:custom"

// Focusable is reached by TUI.setFocus's runtime component contract even though
// TypeScript's Component type does not declare that intersection.
const runtimeReachableFocusable = "pkg:tui/.#Focusable"

var reviewedAmbiguousTypes = map[string]string{
	"KeybindingsManager": "pkg:coding-agent/.#KeybindingsManager",
	"ThinkingLevel":      "pkg:ai/.#ThinkingLevel",
	"TuiMode":            "pkg:tui/.#TuiMode",
}

var exportedTypeReference = regexp.MustCompile(`\b[A-Z][A-Za-z0-9_]*\b`)

type inventory struct {
	UpstreamVersion string          `json:"upstreamVersion"`
	Interfaces      []inventoryItem `json:"interfaces"`
}

type inventoryItem struct {
	ID        string          `json:"id"`
	Kind      string          `json:"kind"`
	ShapeHash string          `json:"shapeHash"`
	Shape     json.RawMessage `json:"shape"`
	Source    source          `json:"source"`
}

type source struct {
	Path   string `json:"path"`
	Line   int    `json:"line"`
	Column int    `json:"column"`
}

type ledger struct {
	UpstreamVersion    string              `json:"upstreamVersion"`
	Status             string              `json:"status"`
	CompatibilityClaim string              `json:"compatibilityClaim"`
	Source             string              `json:"source"`
	PinnedSources      []string            `json:"pinnedSources"`
	PinnedExampleScope []string            `json:"pinnedExampleScope"`
	Factory            factoryContract     `json:"factoryContract"`
	ThemeDomains       themeDomains        `json:"themeDomains"`
	Members            []member            `json:"members"`
	RuntimeReachable   []member            `json:"runtimeReachableMembers"`
	RuntimeOnly        []runtimeOnlyMember `json:"runtimeOnlyMembers"`
}

type factoryContract struct {
	Signature     string   `json:"signature"`
	ArgumentOrder []string `json:"argumentOrder"`
	Timing        string   `json:"timing"`
	Identity      string   `json:"identity"`
	Completion    string   `json:"completion"`
	Result        string   `json:"result"`
	Error         string   `json:"error"`
	Disposal      string   `json:"disposal"`
	Citations     []string `json:"citations"`
}

type themeDomains struct {
	Foreground []string `json:"foreground"`
	Background []string `json:"background"`
	ColorModes []string `json:"colorModes"`
	Reset      string   `json:"reset"`
	Citation   string   `json:"citation"`
}

type compatibility struct {
	FullNodeSource string `json:"fullNodeSource"`
	PinnedExamples string `json:"pinnedExamples"`
	LanguageNative string `json:"languageNative"`
}

type realizationDisposition struct {
	Name     string `json:"name"`
	Status   string `json:"status"`
	Evidence string `json:"evidence"`
}

type member struct {
	ID                     string                   `json:"id"`
	Root                   string                   `json:"root"`
	Kind                   string                   `json:"kind"`
	ShapeHash              string                   `json:"shapeHash"`
	Shape                  json.RawMessage          `json:"shape"`
	PublishedSource        source                   `json:"publishedSource"`
	ImplementationCitation string                   `json:"implementationCitation"`
	Reachability           string                   `json:"reachability"`
	Optionality            string                   `json:"optionality"`
	Timing                 string                   `json:"timing"`
	Behavior               string                   `json:"behavior"`
	ReturnBehavior         string                   `json:"returnBehavior"`
	Identity               string                   `json:"identity"`
	ErrorBehavior          string                   `json:"errorBehavior"`
	Safety                 string                   `json:"safety"`
	Disposition            string                   `json:"pigDisposition"`
	IntendedDisposition    string                   `json:"intendedDisposition"`
	PigTargets             []string                 `json:"pigTargets"`
	Realizations           []realizationDisposition `json:"productionRealizations"`
	Compatibility          compatibility            `json:"compatibility"`
}

type runtimeOnlyMember struct {
	Name         string `json:"name"`
	Signature    string `json:"signature"`
	Reachability string `json:"reachability"`
	Disposition  string `json:"pigDisposition"`
	Citation     string `json:"citation"`
}

func main() {
	check := flag.Bool("check", false, "fail if the checked-in ledger differs")
	input := flag.String("input", filepath.Join("parity", "interfaces", "upstream-v"+coding.UpstreamVersion+".json"), "upstream inventory")
	output := flag.String("output", filepath.Join("parity", "interfaces", outputName), "ledger path")
	flag.Parse()
	data, err := generate(*input)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if *check {
		if err := verify(*output, data); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if err := os.WriteFile(*output, data, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func verify(path string, generated []byte) error {
	checkedIn, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if !bytes.Equal(checkedIn, generated) {
		return fmt.Errorf("%s: drift; regenerate with go run ./parity/cmd/customfactoryledger", path)
	}
	return nil
}

func generate(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var in inventory
	if err := json.Unmarshal(data, &in); err != nil {
		return nil, fmt.Errorf("decode inventory: %w", err)
	}
	if in.UpstreamVersion != coding.UpstreamVersion {
		return nil, fmt.Errorf("inventory version %q does not match coding.UpstreamVersion %q", in.UpstreamVersion, coding.UpstreamVersion)
	}
	byID := make(map[string]inventoryItem, len(in.Interfaces))
	for _, item := range in.Interfaces {
		if _, exists := byID[item.ID]; exists {
			return nil, fmt.Errorf("duplicate inventory interface %s", item.ID)
		}
		byID[item.ID] = item
	}
	roots, err := sourceDerivedRoots(in.Interfaces, byID)
	if err != nil {
		return nil, err
	}
	members, err := classifyRoots(roots, in.Interfaces, byID)
	if err != nil {
		return nil, err
	}
	runtimeReachable, err := classifyRoots([]string{runtimeReachableFocusable}, in.Interfaces, byID)
	if err != nil {
		return nil, err
	}
	for i := range runtimeReachable {
		runtimeReachable[i].Reachability = "exported-interface; runtime-reachable through TuiBase.setFocus structural Focusable check"
	}

	out := ledger{
		UpstreamVersion:    coding.UpstreamVersion,
		Status:             "internal-draft",
		CompatibilityClaim: "none; this draft shapes the private seam and does not assert public source compatibility",
		Source:             "parity/interfaces/upstream-v" + coding.UpstreamVersion + ".json",
		PinnedSources: []string{
			"packages/tui/src/tui.ts",
			"packages/tui/src/terminal.ts",
			"packages/coding-agent/src/core/extensions/types.ts",
			"packages/coding-agent/src/core/keybindings.ts",
			"packages/coding-agent/src/modes/interactive/interactive-mode.ts",
			"packages/coding-agent/src/modes/interactive/theme/theme.ts",
		},
		PinnedExampleScope: []string{
			"packages/coding-agent/examples/extensions/overlay-qa-tests.ts",
			"packages/coding-agent/examples/extensions/overlay-test.ts",
		},
		Factory: factoryContract{
			Signature:     factorySignature(byID[customFactoryRoot]),
			ArgumentOrder: []string{"tui", "theme", "keybindings", "done"},
			Timing:        "the factory may return synchronously or by Promise; the caller awaits factory creation and then awaits done",
			Identity:      "one TUI, stable Theme proxy, configured KeybindingsManager, and exactly-once completion closure are passed in that order",
			Completion:    "the first done(result) removes the append-stack tail in overlay mode before Promise settlement; later done calls are ignored",
			Result:        "done(result) fulfills Promise<T>; omitted JavaScript values are undefined, object identity remains local, and subprocess encoding is a later contract",
			Error:         "a synchronous throw or rejected factory Promise rejects while open; rejection after winning completion cannot reopen or replace the result",
			Disposal:      "after an installed component completes, optional dispose runs once after presentation cleanup; disposal errors do not replace the winning result",
			Citations: []string{
				"packages/coding-agent/src/core/extensions/types.ts:195-210",
				"packages/coding-agent/src/modes/interactive/interactive-mode.ts:2642-2716",
			},
		},
		ThemeDomains: themeDomains{
			Foreground: []string{"accent", "border", "borderAccent", "borderMuted", "success", "error", "warning", "muted", "dim", "text", "thinkingText", "userMessageText", "customMessageText", "customMessageLabel", "toolTitle", "toolOutput", "mdHeading", "mdLink", "mdLinkUrl", "mdCode", "mdCodeBlock", "mdCodeBlockBorder", "mdQuote", "mdQuoteBorder", "mdHr", "mdListBullet", "toolDiffAdded", "toolDiffRemoved", "toolDiffContext", "syntaxComment", "syntaxKeyword", "syntaxFunction", "syntaxVariable", "syntaxString", "syntaxNumber", "syntaxType", "syntaxOperator", "syntaxPunctuation", "thinkingOff", "thinkingMinimal", "thinkingLow", "thinkingMedium", "thinkingHigh", "thinkingXhigh", "thinkingMax", "bashMode"},
			Background: []string{"selectedBg", "scrollbarThumb", "userMessageBg", "customMessageBg", "toolPendingBg", "toolSuccessBg", "toolErrorBg"},
			ColorModes: []string{"truecolor", "256color"},
			Reset:      "style helpers wrap output with the selected ANSI prefix and a reset that restores the surrounding style domain",
			Citation:   "packages/coding-agent/src/modes/interactive/theme/theme.ts:110-165,338-445,813-893",
		},
		Members:          members,
		RuntimeReachable: runtimeReachable,
		RuntimeOnly: []runtimeOnlyMember{
			{Name: "TuiBase.getFocusedComponent", Signature: "() => Component | null", Reachability: "class-runtime-only; absent from exported TUI interface", Disposition: "do-not-promote-without-review", Citation: "packages/tui/src/tui.ts:415-417"},
			{Name: "TuiBase.hasOverlayEntries", Signature: "readonly boolean", Reachability: "class-runtime-only; absent from exported TUI interface", Disposition: "host-internal-mounted-any-query", Citation: "packages/tui/src/tui.ts:354-360"},
		},
	}
	encoded, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(encoded, '\n'), nil
}

func classifyRoots(roots []string, items []inventoryItem, byID map[string]inventoryItem) ([]member, error) {
	var members []member
	var unclassified []string
	for _, root := range roots {
		base, ok := byID[root]
		if !ok {
			return nil, fmt.Errorf("required interface %s is missing", root)
		}
		classified := classify(root, root, base)
		if classified.Disposition == "" {
			unclassified = append(unclassified, root)
		}
		members = append(members, classified)
		prefix := root + "::"
		for _, item := range items {
			if !strings.HasPrefix(item.ID, prefix) {
				continue
			}
			classified := classify(root, item.ID, item)
			if classified.Disposition == "" {
				unclassified = append(unclassified, item.ID)
			}
			members = append(members, classified)
		}
	}
	if len(unclassified) > 0 {
		slices.Sort(unclassified)
		return nil, fmt.Errorf("unclassified custom-factory member %s", strings.Join(slices.Compact(unclassified), ", "))
	}
	slices.SortFunc(members, func(a, b member) int { return strings.Compare(a.ID, b.ID) })
	return members, nil
}

func sourceDerivedRoots(items []inventoryItem, byID map[string]inventoryItem) ([]string, error) {
	if _, ok := byID[customFactoryRoot]; !ok {
		return nil, fmt.Errorf("required interface %s is missing", customFactoryRoot)
	}

	byName := make(map[string][]string)
	for _, item := range items {
		if strings.Contains(item.ID, "::") {
			continue
		}
		_, name, ok := strings.Cut(item.ID, "#")
		if !ok || name == "" {
			continue
		}
		byName[name] = append(byName[name], item.ID)
	}
	for name := range byName {
		slices.Sort(byName[name])
	}

	seen := map[string]bool{customFactoryRoot: true}
	queue := []string{customFactoryRoot}
	for len(queue) > 0 {
		root := queue[0]
		queue = queue[1:]
		for _, item := range items {
			if item.ID != root && !strings.HasPrefix(item.ID, root+"::") {
				continue
			}
			for _, name := range exportedTypeReference.FindAllString(string(item.Shape), -1) {
				candidates := byName[name]
				if len(candidates) == 0 {
					continue
				}
				resolved := ""
				if len(candidates) == 1 {
					resolved = candidates[0]
				} else {
					resolved = reviewedAmbiguousTypes[name]
					if resolved == "" || !slices.Contains(candidates, resolved) {
						return nil, fmt.Errorf("ambiguous exported type reference %s from %s: %s", name, item.ID, strings.Join(candidates, ", "))
					}
				}
				if !seen[resolved] {
					seen[resolved] = true
					queue = append(queue, resolved)
				}
			}
		}
	}
	return slices.Sorted(maps.Keys(seen)), nil
}

func factorySignature(item inventoryItem) string {
	var shape struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(item.Shape, &shape) != nil {
		return ""
	}
	return shape.Type
}

func classify(root, id string, item inventoryItem) member {
	shapeType, returns := shapeDetails(item.Shape)
	semantic, ok := reviewedMember(root, id)
	if !ok {
		return member{ID: id, Root: root, Kind: item.Kind, ShapeHash: item.ShapeHash, Shape: item.Shape}
	}
	identity := memberIdentity(root, memberProperty(id))
	safety := "factory-safe-after-source-review"
	if isUnsafeOwnership(root, id) {
		safety = "unsafe-host-lifecycle-or-terminal-ownership"
	}
	example := "not-exercised-by-pinned-overlay-examples"
	if exampleMember(id) {
		example = "exercised-by-pinned-overlay-examples"
	}
	realizations := realizationDispositions(root, safety == "unsafe-host-lifecycle-or-terminal-ownership")
	return member{
		ID:                     id,
		Root:                   root,
		Kind:                   item.Kind,
		ShapeHash:              item.ShapeHash,
		Shape:                  item.Shape,
		PublishedSource:        item.Source,
		ImplementationCitation: memberCitation(root, memberProperty(id)),
		Reachability:           "exported-interface",
		Optionality:            optionalitySummary(item.Shape),
		Timing:                 semantic.timing,
		Behavior:               semantic.behavior,
		ReturnBehavior:         declaredReturnBehavior(item.Kind, shapeType, returns),
		Identity:               identity,
		ErrorBehavior:          semantic.error,
		Safety:                 safety,
		Disposition:            currentPigDisposition(root, memberProperty(id)),
		IntendedDisposition:    semantic.disposition,
		PigTargets:             pigTargets(root),
		Realizations:           realizations,
		Compatibility: compatibility{
			FullNodeSource: "required-before-public-claim",
			PinnedExamples: example,
			LanguageNative: "equivalent-observable-behavior-required",
		},
	}
}

func shapeDetails(raw json.RawMessage) (shapeType string, returns string) {
	var shape struct {
		Type    string `json:"type"`
		Returns string `json:"returns"`
		Calls   []struct {
			Returns string `json:"returns"`
		} `json:"calls"`
	}
	_ = json.Unmarshal(raw, &shape)
	if shape.Returns == "" && len(shape.Calls) == 1 {
		shape.Returns = shape.Calls[0].Returns
	}
	return shape.Type, shape.Returns
}

func declaredReturnBehavior(kind, shapeType, returns string) string {
	if returns != "" {
		return "exact call result: " + returns
	}
	if kind == "property" {
		return "exact property type: " + shapeType
	}
	return "exact declaration type: " + shapeType
}

func optionalitySummary(raw json.RawMessage) string {
	var shape struct {
		Optional   bool `json:"optional"`
		Parameters []struct {
			Name     string `json:"name"`
			Optional bool   `json:"optional"`
			Rest     bool   `json:"rest"`
		} `json:"parameters"`
		Calls []struct {
			Parameters []struct {
				Name     string `json:"name"`
				Optional bool   `json:"optional"`
				Rest     bool   `json:"rest"`
			} `json:"parameters"`
		} `json:"calls"`
	}
	_ = json.Unmarshal(raw, &shape)
	parameters := shape.Parameters
	if len(parameters) == 0 && len(shape.Calls) == 1 {
		parameters = shape.Calls[0].Parameters
	}
	parts := []string{"member=required"}
	if shape.Optional {
		parts[0] = "member=optional (omission and undefined distinct)"
	}
	for _, parameter := range parameters {
		state := "required"
		if parameter.Optional {
			state = "optional (omission and undefined distinct)"
		}
		if parameter.Rest {
			state = "rest"
		}
		parts = append(parts, parameter.Name+"="+state)
	}
	return strings.Join(parts, "; ")
}

func isUnsafeOwnership(root, id string) bool {
	if root == "pkg:tui/.#Terminal" {
		return true
	}
	unsafeSuffixes := []string{
		"#TUI::property:terminal",
		"#TUI::property:start",
		"#TUI::property:stop",
		"#TUI::property:clear",
		"#TUI::property:setFocus",
		"#TUI::property:setShowHardwareCursor",
		"#TUI::property:setClearOnShrink",
	}
	for _, suffix := range unsafeSuffixes {
		if strings.HasSuffix(id, suffix) {
			return true
		}
	}
	return false
}

func exampleMember(id string) bool {
	for _, fragment := range []string{
		"#Component", "#Focusable", "#OverlayOptions", "#OverlayHandle", "#OverlayUnfocusOptions", "#SizeValue",
		"#Theme", "#ExtensionUIContext::property:custom", "#TUI::property:showOverlay", "#TUI::property:requestRender",
	} {
		if strings.Contains(id, fragment) {
			return true
		}
	}
	return false
}
