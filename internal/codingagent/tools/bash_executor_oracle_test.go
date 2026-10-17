package tools

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

type bashExecScenario struct {
	Chunks     []string `json:"chunks"`
	AbortAfter int      `json:"abortAfter"`
	UseSignal  bool     `json:"useSignal"`
	ExitCode   *int     `json:"exitCode"`
	Error      *string  `json:"error"`
}

type bashExecOutcome struct {
	Chunks    []string `json:"chunks"`
	Output    string   `json:"output"`
	ExitCode  *int     `json:"exitCode"`
	Cancelled bool     `json:"cancelled"`
	Truncated bool     `json:"truncated"`
	File      *string  `json:"file"`
	Thrown    *string  `json:"thrown"`
}

func bashExecScenarios() []bashExecScenario {
	rng := rand.New(rand.NewPCG(31, 4))
	pick := func(items []string) string { return items[rng.IntN(len(items))] }
	pieces := []string{"hello", " ", "world", "\n", "\n", "line of output\n", "é", "日本語", "😀", "\xff", "\xc3", "\xe6\x97", "\r", "\r\n", "\t", "\x00", "\x07", "\x08", "\x1b[31m", "\x1b[0m", "\x1b]0;title\x07", "\x1b[", "\x1b", "\x1b[38;5;200m", "\x1b[2K", "\x7f", "x\n", "0123456789", "\u2028", "\x1b\\", "\x1b[1;2", "\xf0\x9f", "\x9b31m"}
	var scenarios []bashExecScenario
	for range 900 {
		var stream []byte
		// A BOM is generated only first in the stream: Node's TextDecoder also drops a BOM that follows a first chunk ending inside a multi-byte character (Q17), which the WHATWG decoder Pig implements keeps.
		if rng.IntN(6) == 0 {
			stream = append(stream, "\xef\xbb\xbf"...)
		}
		target := []int{0, 40, 600, 5000, 30000, 60000, 130000, 260000, 14000}[rng.IntN(9)]
		short := rng.IntN(5) == 0
		for len(stream) < target {
			if short {
				stream = append(stream, "x\n"...)
				continue
			}
			stream = append(stream, pick(pieces)...)
			if rng.IntN(4) == 0 {
				stream = append(stream, "some longer plain text to fill the buffer faster "...)
			}
		}
		chunks := []string{}
		for rest := stream; len(rest) > 0; {
			size := min(1+rng.IntN([]int{3, 40, 700, 9000, 70000}[rng.IntN(5)]), len(rest))
			chunks = append(chunks, hex.EncodeToString(rest[:size]))
			rest = rest[size:]
		}
		scenario := bashExecScenario{Chunks: chunks, AbortAfter: -2, UseSignal: rng.IntN(5) != 0}
		if len(chunks) > 0 && rng.IntN(4) == 0 {
			scenario.AbortAfter = rng.IntN(len(chunks))
		} else if rng.IntN(20) == 0 {
			scenario.AbortAfter = -1
		}
		switch rng.IntN(6) {
		case 0:
			message := pick([]string{"spawn failed", "", "ünï\ncode"})
			scenario.Error = &message
		case 1:
			// exit code null: a failed command
		default:
			code := []int{0, 0, 1, 2, 127, 130, 255}[rng.IntN(7)]
			scenario.ExitCode = &code
		}
		scenarios = append(scenarios, scenario)
	}
	// Totals on each side of the 51200-byte spill threshold and the 102400-character rolling window, delivered as one chunk and as two, so each comparison is decided by one byte.
	for _, total := range []int{51199, 51200, 51201, 102399, 102400, 102401, 102402} {
		for _, split := range []int{0, 1, 2} {
			text := strings.Repeat("a", total-1) + "\n"
			var chunks []string
			switch split {
			case 0:
				chunks = []string{hex.EncodeToString([]byte(text))}
			case 1:
				chunks = []string{hex.EncodeToString([]byte(text[:total/2])), hex.EncodeToString([]byte(text[total/2:]))}
			default:
				chunks = []string{hex.EncodeToString([]byte(text[:min(51200, total-1)])), hex.EncodeToString([]byte(text[min(51200, total-1):]))}
			}
			code := 0
			scenarios = append(scenarios, bashExecScenario{Chunks: chunks, AbortAfter: -2, UseSignal: true, ExitCode: &code})
		}
	}
	return scenarios
}

type scriptedBashOperations struct {
	scenario bashExecScenario
	cancel   context.CancelFunc
}

func (s scriptedBashOperations) Exec(_ context.Context, _, _ string, options BashOperationsExecOptions) (BashOperationsResult, error) {
	if s.scenario.AbortAfter == -1 {
		s.cancel()
	}
	for i, chunk := range s.scenario.Chunks {
		data, _ := hex.DecodeString(chunk)
		options.OnData(data)
		if s.scenario.AbortAfter == i {
			s.cancel()
		}
	}
	if s.scenario.Error != nil {
		return BashOperationsResult{}, errors.New(*s.scenario.Error)
	}
	return BashOperationsResult{ExitCode: s.scenario.ExitCode}, nil
}

// executeBashWithOperations (core/bash-executor.ts) against pinned Pi over 900 seeded scripted runs: chunk boundaries inside multi-byte characters and escape sequences, ANSI and OSC sequences, carriage returns, NUL and other control bytes, invalid UTF-8, a leading byte-order mark, outputs from empty to 260 KB (past the 50 KB spill to a file and the 100 KB rolling window), more than 2000 lines, aborts before, during and after the output, error throws, null and nonzero exit codes. Compared: every onChunk call, the result and the spilled file's content.
func TestExecuteBashWithOperationsMatchesPi(t *testing.T) {
	scenarios := bashExecScenarios()
	input, err := json.Marshal(scenarios)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/bash_executor.mjs", pigversion.UpstreamPackageVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var want []bashExecOutcome
	if err := json.Unmarshal(output, &want); err != nil {
		t.Fatal(err)
	}
	mismatches, spilled, truncated, cancelled, thrown := 0, 0, 0, 0, 0
	for i, scenario := range scenarios {
		ctx, cancel := context.WithCancel(t.Context())
		runCtx := ctx
		if !scenario.UseSignal {
			// Pi passes no signal: its abort() cannot be seen by the executor.
			runCtx = context.WithoutCancel(ctx)
		}
		var chunks []string
		result, runErr := ExecuteBashWithOperations(runCtx, "cmd", "/cwd", scriptedBashOperations{scenario, cancel}, BashExecOptions{OnChunk: func(c string) { chunks = append(chunks, c) }})
		cancel()
		w := want[i]
		var got bashExecOutcome
		got.Chunks = chunks
		if runErr != nil {
			message := runErr.Error()
			got.Thrown = &message
		} else {
			got.Output, got.ExitCode, got.Cancelled, got.Truncated = result.Output, result.ExitCode, result.Cancelled, result.Truncated
			if result.FullOutputPath != "" {
				data, readErr := os.ReadFile(result.FullOutputPath)
				text := string(data)
				if readErr != nil {
					text = "<missing>"
				}
				got.File = &text
				_ = os.Remove(result.FullOutputPath)
			}
		}
		if got.Chunks == nil {
			got.Chunks = []string{}
		}
		if w.Chunks == nil {
			w.Chunks = []string{}
		}
		switch {
		case w.Thrown != nil:
			thrown++
		case w.Truncated:
			truncated++
		}
		if w.File != nil {
			spilled++
		}
		if w.Cancelled {
			cancelled++
		}
		if !sameBashOutcome(got, w) {
			if mismatches++; mismatches <= 6 {
				t.Errorf("scenario %d (%d chunks, abortAfter %d, signal %v): %s", i, len(scenario.Chunks), scenario.AbortAfter, scenario.UseSignal, describeBashDiff(got, w))
			}
		}
	}
	if mismatches > 6 {
		t.Errorf("%d of %d scenarios differ from Pi", mismatches, len(scenarios))
	}
	if spilled < 100 || truncated < 50 || cancelled < 50 || thrown < 50 {
		t.Errorf("thin coverage: spilled %d truncated %d cancelled %d thrown %d", spilled, truncated, cancelled, thrown)
	}
}

func sameBashOutcome(got, want bashExecOutcome) bool {
	if (got.Thrown == nil) != (want.Thrown == nil) || (got.Thrown != nil && *got.Thrown != *want.Thrown) {
		return false
	}
	if len(got.Chunks) != len(want.Chunks) {
		return false
	}
	for i := range got.Chunks {
		if got.Chunks[i] != want.Chunks[i] {
			return false
		}
	}
	if want.Thrown != nil {
		return true
	}
	if (got.ExitCode == nil) != (want.ExitCode == nil) || (got.ExitCode != nil && *got.ExitCode != *want.ExitCode) {
		return false
	}
	if (got.File == nil) != (want.File == nil) || (got.File != nil && *got.File != *want.File) {
		return false
	}
	return got.Output == want.Output && got.Cancelled == want.Cancelled && got.Truncated == want.Truncated
}

func describeBashDiff(got, want bashExecOutcome) string {
	clip := func(s string) string {
		if len(s) > 80 {
			return fmt.Sprintf("%q…(%d bytes)", s[:80], len(s))
		}
		return fmt.Sprintf("%q", s)
	}
	var parts []string
	if (got.Thrown == nil) != (want.Thrown == nil) || (got.Thrown != nil && *got.Thrown != *want.Thrown) {
		parts = append(parts, fmt.Sprintf("thrown %v vs Pi %v", got.Thrown, want.Thrown))
	}
	if len(got.Chunks) != len(want.Chunks) {
		parts = append(parts, fmt.Sprintf("chunks %d vs Pi %d", len(got.Chunks), len(want.Chunks)))
	} else {
		for i := range got.Chunks {
			if got.Chunks[i] != want.Chunks[i] {
				parts = append(parts, fmt.Sprintf("chunk %d %s vs Pi %s", i, clip(got.Chunks[i]), clip(want.Chunks[i])))
				break
			}
		}
	}
	if got.Output != want.Output {
		parts = append(parts, fmt.Sprintf("output %s vs Pi %s", clip(got.Output), clip(want.Output)))
	}
	if got.Cancelled != want.Cancelled || got.Truncated != want.Truncated {
		parts = append(parts, fmt.Sprintf("cancelled/truncated %v/%v vs Pi %v/%v", got.Cancelled, got.Truncated, want.Cancelled, want.Truncated))
	}
	if (got.File == nil) != (want.File == nil) || (got.File != nil && *got.File != *want.File) {
		g, w := "<none>", "<none>"
		if got.File != nil {
			g = clip(*got.File)
		}
		if want.File != nil {
			w = clip(*want.File)
		}
		parts = append(parts, fmt.Sprintf("file %s vs Pi %s", g, w))
	}
	if (got.ExitCode == nil) != (want.ExitCode == nil) || (got.ExitCode != nil && *got.ExitCode != *want.ExitCode) {
		parts = append(parts, fmt.Sprintf("exit %v vs Pi %v", got.ExitCode, want.ExitCode))
	}
	return strings.Join(parts, "; ")
}
