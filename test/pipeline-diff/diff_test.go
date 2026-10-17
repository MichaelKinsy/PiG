package pipelinediff

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/requestvalidity"
)

// outcome is the verdict of one case.
type outcome struct {
	Kind   string // "equal", "differs", "pi-error", "pig-error", "no-request"
	Detail string
	// PiViolations and PigViolations are the contract violations of each side's request.
	PiViolations  []string
	PigViolations []string
}

type harness struct {
	// direct skips agent.NormalizeMessages on PiG's side.
	direct  bool
	t       testing.TB
	capture *capture
	pi      *piWorker
}

func newHarness(t testing.TB) *harness {
	return &harness{t: t, capture: newCapture(t.(*testing.T)), pi: startPiWorker(t)}
}

func (h *harness) judge(c testCase) outcome {
	piURL, piKey := h.capture.slot("pi")
	piStreamErr, piErr := h.pi.runWithStatus(c, piURL)
	piBody, piOK := h.capture.take(piKey)
	pigURL, pigKey := h.capture.slot("pig")
	pigViolations, pigStreamErr, pigErr := pigRun(c, pigURL, !h.direct)
	pigBody, pigOK := h.capture.take(pigKey)
	if piErr == nil && !piOK && piStreamErr != "" {
		piErr = errors.New(piStreamErr)
	}
	if pigErr == nil && !pigOK && pigStreamErr != "" {
		pigErr = errors.New(pigStreamErr)
	}
	switch {
	case !piOK && !pigOK && piErr != nil && pigErr != nil:
		return outcome{Kind: "equal", Detail: "both fail before sending"}
	case !piOK && piErr != nil:
		return outcome{Kind: "pi-error", Detail: piErr.Error()}
	case !pigOK && pigErr != nil:
		return outcome{Kind: "pig-error", Detail: pigErr.Error()}
	case piOK != pigOK:
		return outcome{Kind: "no-request", Detail: fmt.Sprintf("pi sent a request: %v, pig sent a request: %v", piOK, pigOK)}
	case !piOK:
		return outcome{Kind: "equal", Detail: "neither sent"}
	}
	piProj, err := projection(c.Target.API, piBody)
	if err != nil {
		return outcome{Kind: "pi-error", Detail: err.Error()}
	}
	if contents, ok := piProj["contents"].([]any); ok {
		piProj["contents"] = withD115(contents)
	}
	pigProj, err := projection(c.Target.API, pigBody)
	if err != nil {
		return outcome{Kind: "pig-error", Detail: err.Error()}
	}
	out := outcome{Kind: "equal", PigViolations: pigViolations}
	for _, v := range requestvalidity.CheckBody(c.Target.API, piBody) {
		out.PiViolations = append(out.PiViolations, v.String())
	}
	// Where Pi's own request breaks the provider's contract on a Chaos history, PiG's repair of the history is the intended difference (D48 and the late-result recovery); only PiG's validity is required there. A Realistic history holds nothing D48 repairs, so its requests are compared even when Pi's breaks a rule (the Google image split).
	if d := difference(piProj, pigProj); d != "" && (len(out.PiViolations) == 0 || h.direct || c.Mode == Realistic) {
		out.Kind, out.Detail = "differs", d
	}
	return out
}

// defaultCases is the number of cases per target and mode: a small budget for the cheap gate, a larger one in CI.
func defaultCases() int {
	if os.Getenv("CI") != "" {
		return 150
	}
	return 12
}

func envInt(name string, fallback int) int {
	if v, err := strconv.Atoi(os.Getenv(name)); err == nil {
		return v
	}
	return fallback
}

// sharedWithPi reports whether every rule PiG's request breaks is also broken by Pi's request, or is the Google image split below: PiG then matches a flaw of Pi's pipeline instead of adding one.
func sharedWithPi(c testCase, o outcome) bool {
	allowed := map[string]bool{}
	for _, v := range o.PiViolations {
		allowed[violationRule(v)] = true
	}
	// An orphan result is never PiG's to send: D48 drops it. Only the Google image split can leave one.
	delete(allowed, "result-has-call")
	if slices.ContainsFunc(o.PiViolations, func(v string) bool { return violationRule(v) == "result-has-call" }) {
		// Pi's request holds an orphan result, which D48 drops. Dropping it can leave two turns of one role side by side or end the request with an assistant message; Pi's request is rejected for the orphan either way.
		for _, rule := range []string{"call-turn-order", "last-role", "role-alternation", "first-role"} {
			allowed[rule] = true
		}
	}
	for _, v := range o.PigViolations {
		if !allowed[violationRule(v)] {
			return false
		}
	}
	return true
}

// withD115 rewrites Pi's Google contents into the shape D115 specifies. Pi 1.1.0 (google-shared.ts convertMessages) pushes the image turn of a Gemini 2.x tool result ("Tool result image:") before the next result, which splits the function-response turn of parallel calls; PiG keeps every function response of a run of results in the run's first turn and puts the image turns after it, in result order.
func withD115(contents []any) []any {
	kind := func(turn any) string {
		m, _ := turn.(map[string]any)
		parts, _ := m["parts"].([]any)
		if m["role"] != "user" || len(parts) == 0 {
			return ""
		}
		first, _ := parts[0].(map[string]any)
		if _, ok := first["functionResponse"]; ok {
			return "response"
		}
		if first["text"] == "Tool result image:" {
			return "image"
		}
		return ""
	}
	var out, images []any
	var run map[string]any
	flush := func() {
		out = append(out, images...)
		images, run = nil, nil
	}
	for _, turn := range contents {
		switch kind(turn) {
		case "response":
			if run == nil {
				run = turn.(map[string]any)
				out = append(out, turn)
				continue
			}
			run["parts"] = append(run["parts"].([]any), turn.(map[string]any)["parts"].([]any)...)
		case "image":
			if run == nil {
				out = append(out, turn)
				continue
			}
			images = append(images, turn)
		default:
			flush()
			out = append(out, turn)
		}
	}
	flush()
	return out
}

// TestPipelineMatchesPi sends seeded random histories through Pi 1.1.0's pipeline (coding-agent convertToLlm, transformMessages, the API's converter) and through PiG's, and compares the requests each puts on the wire.
//
// Realistic histories are what Pi's agent and session produce. For them PiG's request must equal Pi's, even where Pi's breaks the provider's contract, and must meet the contract except for a violation Pi's request has too (a flaw PiG matches, logged as such).
// Chaos histories also hold shapes only a damaged session has. PiG deliberately repairs those where Pi sends a request the provider rejects (D48 and the late-result recovery), so the requests are compared only where Pi's own request meets the contract; PiG's request must meet it always.
// PIG_PIPELINE_CASES sets the cases per target and mode (the default is the cheap-gate budget), PIG_PIPELINE_SEED the first seed.
func TestPipelineMatchesPi(t *testing.T) {
	h := newHarness(t)
	cases := envInt("PIG_PIPELINE_CASES", defaultCases())
	first := uint64(envInt("PIG_PIPELINE_SEED", 1))
	for _, mode := range []Mode{Realistic, Chaos} {
		for _, tgt := range targets {
			shared := 0
			rules := map[string]int{}
			for i := range cases {
				c := generate(first+uint64(i), tgt, mode)
				o := h.judge(c)
				if o.Kind == "equal" && len(o.PigViolations) == 0 {
					continue
				}
				if o.Kind == "equal" && sharedWithPi(c, o) {
					shared++
					for _, v := range o.PigViolations {
						rules[violationRule(v)]++
					}
					continue
				}
				sig := signature(o)
				small := shrink(c, func(candidate testCase) bool {
					if !sendable(candidate) || (mode == Realistic && !realistic(candidate)) {
						return false
					}
					return judgedBad(candidate, h.judge(candidate), mode)
				})
				final := h.judge(small)
				encoded, _ := json.MarshalIndent(small, "", " ")
				t.Errorf("%s %s seed=%d (%s): %s\npig violations: %v\npi violations: %v\nsmallest case (save as JSON and set PIG_PIPELINE_CASE_FILE to run TestShow):\n%s", tgt.Name, modeName(mode), c.Seed, sig, truncate(final.Detail, 600), final.PigViolations, final.PiViolations, encoded)
				break
			}
			if shared > 0 {
				t.Logf("%s %s: %d of %d cases have a request that breaks the provider contract in Pi's pipeline too (PiG violations by rule: %v)", tgt.Name, modeName(mode), shared, cases, rules)
			}
		}
	}
}

func modeName(m Mode) string {
	if m == Chaos {
		return "chaos"
	}
	return "realistic"
}

// judgedBad is the failure predicate of TestPipelineMatchesPi.
func judgedBad(c testCase, o outcome, mode Mode) bool {
	switch o.Kind {
	case "differs", "no-request", "pig-error":
		return true
	case "pi-error":
		return false
	}
	if len(o.PigViolations) == 0 || sharedWithPi(c, o) {
		return false
	}
	return true
}

// TestAIPipelineMatchesPi is TestPipelineMatchesPi without agent.NormalizeMessages, which has no counterpart in Pi: convertToLlm, the ai package's transformMessages and the API converters alone must put the same request on the wire as Pi's, for every history, damaged or not.
func TestAIPipelineMatchesPi(t *testing.T) {
	h := newHarness(t)
	h.direct = true
	cases := envInt("PIG_PIPELINE_CASES", defaultCases())
	first := uint64(envInt("PIG_PIPELINE_SEED", 1))
	for _, mode := range []Mode{Realistic, Chaos} {
		for _, tgt := range targets {
			for i := range cases {
				c := generate(first+uint64(i), tgt, mode)
				o := h.judge(c)
				if o.Kind == "equal" || o.Kind == "pi-error" {
					continue
				}
				small := shrink(c, func(candidate testCase) bool {
					if !sendable(candidate) || (mode == Realistic && !realistic(candidate)) {
						return false
					}
					got := h.judge(candidate)
					return got.Kind != "equal" && got.Kind != "pi-error"
				})
				final := h.judge(small)
				encoded, _ := json.MarshalIndent(small, "", " ")
				t.Errorf("%s %s seed=%d (%s): %s\nsmallest case (save as JSON and set PIG_PIPELINE_CASE_FILE to run TestShow):\n%s", tgt.Name, modeName(mode), c.Seed, signature(final), truncate(final.Detail, 600), encoded)
				break
			}
		}
	}
}

// TestExplore (PIG_PIPELINE_EXPLORE=1) reports the distinct kinds of difference over PIG_PIPELINE_CASES seeded cases per target, each with its smallest case.
func TestExplore(t *testing.T) {
	if os.Getenv("PIG_PIPELINE_EXPLORE") == "" {
		t.Skip("set PIG_PIPELINE_EXPLORE=1 to explore")
	}
	h := newHarness(t)
	cases := envInt("PIG_PIPELINE_CASES", 20)
	first := uint64(envInt("PIG_PIPELINE_SEED", 1))
	mode := Mode(envInt("PIG_PIPELINE_MODE", 0))
	seen := map[string]bool{}
	counts := map[string]int{}
	for _, tgt := range targets {
		for i := range cases {
			c := generate(first+uint64(i), tgt, mode)
			o := h.judge(c)
			sigs := []string{signature(o)}
			for _, v := range o.PigViolations {
				sigs = append(sigs, "PIG-INVALID "+violationRule(v))
			}
			for _, v := range o.PiViolations {
				sigs = append(sigs, "PI-INVALID "+violationRule(v))
			}
			for _, sig := range sigs {
				if sig == "equal" {
					continue
				}
				counts[tgt.Name+" "+sig]++
				key := tgt.Name + " " + sig
				if seen[key] {
					continue
				}
				seen[key] = true
				small := shrink(c, func(candidate testCase) bool {
					if !sendable(candidate) || (mode == Realistic && !realistic(candidate)) {
						return false
					}
					return slices.Contains(sigsOf(h.judge(candidate)), sig)
				})
				final := h.judge(small)
				encoded, _ := json.Marshal(small.Messages)
				t.Logf("%s | %s\n  seed=%d detail: %s\n  system=%q messages=%s", tgt.Name, sig, c.Seed, truncate(final.Detail, 300), small.SystemPrompt, truncate(string(encoded), 1500))
			}
		}
	}
	t.Logf("counts: %v", counts)
}

func sigsOf(o outcome) []string {
	sigs := []string{signature(o)}
	for _, v := range o.PigViolations {
		sigs = append(sigs, "PIG-INVALID "+violationRule(v))
	}
	for _, v := range o.PiViolations {
		sigs = append(sigs, "PI-INVALID "+violationRule(v))
	}
	return sigs
}

// violationRule is the rule name of an "api rule: detail" violation line.
func violationRule(v string) string {
	fields := strings.FieldsSeq(v)
	for f := range fields {
		if before, ok := strings.CutSuffix(f, ":"); ok {
			return before
		}
	}
	return v
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}

// TestShow prints both requests for the case in PIG_PIPELINE_CASE_FILE (a JSON testCase) or for PIG_PIPELINE_SEED on PIG_PIPELINE_TARGET.
func TestShow(t *testing.T) {
	file := os.Getenv("PIG_PIPELINE_CASE_FILE")
	name := os.Getenv("PIG_PIPELINE_TARGET")
	if file == "" && name == "" {
		t.Skip("set PIG_PIPELINE_CASE_FILE or PIG_PIPELINE_TARGET to show a case")
	}
	var c testCase
	if file != "" {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(data, &c); err != nil {
			t.Fatal(err)
		}
	} else {
		for _, tgt := range targets {
			if tgt.Name == name {
				c = generate(uint64(envInt("PIG_PIPELINE_SEED", 1)), tgt, Mode(envInt("PIG_PIPELINE_MODE", 0)))
			}
		}
	}
	h := newHarness(t)
	piURL, piKey := h.capture.slot("pi")
	if streamErr, err := h.pi.runWithStatus(c, piURL); err != nil {
		t.Logf("pi error: %v", err)
	} else {
		t.Logf("pi stream error: %s", streamErr)
	}
	pigURL, pigKey := h.capture.slot("pig")
	violations, streamErr, err := pigRun(c, pigURL, !h.direct)
	t.Logf("pig error: %v stream error: %s violations: %v", err, streamErr, violations)
	for _, side := range []struct{ name, key string }{{"PI", piKey}, {"PIG", pigKey}} {
		body, _ := h.capture.take(side.key)
		t.Logf("%s contract violations: %v", side.name, requestvalidity.CheckBody(c.Target.API, body))
		proj, _ := projection(c.Target.API, body)
		pretty, _ := json.MarshalIndent(proj, "", " ")
		t.Logf("%s:\n%s", side.name, pretty)
	}
}
