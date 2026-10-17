package ai

// pi: packages/ai/src/api/google-shared.ts

import (
	"bytes"
	"encoding/json"
	"math/rand"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

type googleModelIDResult struct {
	ThinkingLevel bool    `json:"thinkingLevel"`
	Major         *string `json:"major"`
	ToolCallID    bool    `json:"toolCallId"`
	Multimodal    bool    `json:"multimodal"`
	Strict        bool    `json:"strict"`
}

// googleModelIDProbes build Gemini, Gemma and other model ids from the families and spellings a catalog or models.json carries: versions with and without minors, preview suffixes, live variants, mixed case,
// huge version numbers, and letters whose case folding differs between JavaScript's toLowerCase and Go's regexp (long s, Kelvin sign, dotted capital I, Greek sigma).
func googleModelIDProbes() []string {
	random := rand.New(rand.NewSource(20260952))
	prefixes := []string{"", "", "models/", "google/", "publishers/google/models/", " ", "x-"}
	families := []string{"gemini-", "GEMINI-", "Gemini-", "gemini-live-", "gemini-flash-", "gemma-", "gemma", "GEMMA-", "claude-", "gpt-oss-", "gpt-", "gemın", "GEMİNİ-", "gemini\u2011", "geminiı-"}
	versions := []string{"1", "1.5", "2", "2.0", "2.5", "3", "3.0", "3.1", "3.8", "3.10", "4", "4.5", "10", "007", "0", "99999999999999999999", "123456789012345678901234567890", "3.", "3.x", "٣", "３"}
	kinds := []string{"-pro", "-flash", "-flash-lite", "-pro-preview", "-flash-preview-05-20", "-FLASH", "-Pro", "-fla\u017fh", "-flaſh", "-\u212ero", "-pro\u212a", "-ultra", "", "-4", "4", "-4-it", "4-it", "-latest", "-flash-latest", "-flash-lite-latest"}
	// A version past float64's range is Infinity for parseInt, which still compares as at least 3.
	ids := []string{"gemini-" + strings.Repeat("9", 400) + "-pro", "gemini-live-" + strings.Repeat("1", 320), "", "gemini-flash-latest", "gemini-flash-lite-latest", "GEMINI-FLASH-LATEST", "gemini-flash-latest\u017f", "gemma4", "gemma-4", "gemma--4", "gemma 4", "claude-3-5-sonnet", "gpt-oss-120b", "gemini-live-2.5-flash", "gemini-3-flash-preview", "gemini-3.1-pro-preview"}
	for range 4500 {
		ids = append(ids, prefixes[random.Intn(len(prefixes))]+families[random.Intn(len(families))]+versions[random.Intn(len(versions))]+kinds[random.Intn(len(kinds))])
	}
	return ids
}

// The Gemini id helpers cut from Pi's google-shared.ts run in Node on the same ids: the thinking-level family test, the major version (parseInt, so a huge number stays large), the tool-call-id, multimodal
// and strict-sampling decisions must match Pig's UsesGoogleThinkingLevel, getGeminiMajorVersion, requiresToolCallId, supportsMultimodalFunctionResponse and supportsGoogleStrictToolSampling.
func TestGoogleModelIDHelpersMatchPi(t *testing.T) {
	ids := googleModelIDProbes()
	payload, err := json.Marshal(ids)
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(testenv.ModuleRoot(t), ".upstream", "current", "packages", "ai", "src", "api", "google-shared.ts")
	cmd := exec.CommandContext(t.Context(), "node", "--disable-warning=ExperimentalWarning", "testdata/google_model_ids.mjs", source)
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []googleModelIDResult
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	if len(expected) != len(ids) {
		t.Fatalf("Pi answered %d of %d probes", len(expected), len(ids))
	}
	differing, thinking, versioned, huge := 0, 0, 0, 0
	for i, id := range ids {
		got := googleModelIDResult{
			ThinkingLevel: UsesGoogleThinkingLevel(&Model{ID: id}),
			ToolCallID:    requiresToolCallId(id),
			Multimodal:    supportsMultimodalFunctionResponse(id),
			Strict:        supportsGoogleStrictToolSampling(id),
		}
		if major, ok := getGeminiMajorVersion(id); ok {
			text := strconv.FormatFloat(major, 'f', -1, 64)
			got.Major = &text
		}
		want := expected[i]
		if want.ThinkingLevel {
			thinking++
		}
		if want.Major != nil {
			versioned++
			value, _ := strconv.ParseFloat(*want.Major, 64)
			if value > 1e15 {
				huge++
			}
			// JavaScript prints 1e+21 where Go prints digits: compare the numbers.
			if got.Major != nil {
				if gotValue, _ := strconv.ParseFloat(*got.Major, 64); gotValue == value {
					got.Major = want.Major
				}
			}
		}
		gotJSON, _ := json.Marshal(got)
		wantJSON, _ := json.Marshal(want)
		if !bytes.Equal(gotJSON, wantJSON) {
			if differing++; differing <= 12 {
				t.Errorf("%q\n  Pig %s\n  Pi  %s", id, gotJSON, wantJSON)
			}
		}
	}
	if differing > 12 {
		t.Errorf("%d of %d probes differ from Pi", differing, len(ids))
	}
	if thinking < 100 || versioned < 250 || huge < 20 {
		t.Errorf("probes are one-sided: %d thinking-level, %d versioned, %d huge", thinking, versioned, huge)
	}
}
