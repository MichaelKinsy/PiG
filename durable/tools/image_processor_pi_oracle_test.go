package tools

// pi: packages/durable/src/tools/image-processor.ts (DEFAULT_IMAGE_LIMITS, INLINE_IMAGE_TYPES, toBase64, base64Length)

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// Pi's image-processor.ts holds the limits the coding agent keeps, the formats every provider takes inline, and the two
// helpers that size and build the base64 of an image. Every expectation below is Pi's own answer, from the real file under
// Node: the constants, base64Length at every size from 0 to 600 and at the sizes around the limits of a 4.5 MiB image, and
// toBase64 of the lengths where its 8 KiB blocks begin and end.

type imageProcessorOp struct {
	Op     string   `json:"op"`
	N      int64    `json:"n,omitempty"`
	Length int      `json:"length,omitempty"`
	Step   int      `json:"step,omitempty"`
	Types  []string `json:"types,omitempty"`
}

func TestImageProcessorHelpersMatchPi(t *testing.T) {
	probes := []string{"image/png", "image/jpeg", "image/gif", "image/webp", "image/bmp", "image/svg+xml", "image/avif", "image/tiff", "image/x-icon", "image/jpg", "IMAGE/PNG", "image/png ", "", "png"}
	ops := []imageProcessorOp{{Op: "constants", Types: probes}}
	for n := range int64(601) {
		ops = append(ops, imageProcessorOp{Op: "base64Length", N: n})
	}
	for _, n := range []int64{4718592, 4718593, 3538944, 3538945, 3538946, 3538947, 1 << 30, 1<<31 - 1, 1 << 40} {
		ops = append(ops, imageProcessorOp{Op: "base64Length", N: n})
	}
	for _, length := range []int{0, 1, 2, 3, 4, 5, 8190, 8191, 8192, 8193, 8194, 16383, 16384, 16385, 16386, 24576, 24577, 100000} {
		for _, step := range []int{37, 1, 255} {
			ops = append(ops, imageProcessorOp{Op: "toBase64", Length: length, Step: step})
		}
	}
	input, err := json.Marshal(ops)
	if err != nil {
		t.Fatal(err)
	}
	root := testenv.ModuleRoot(t)
	cmd := exec.CommandContext(t.Context(), "node", "--disable-warning=ExperimentalWarning", "testdata/image_processor_oracle.mjs", filepath.Join(root, ".upstream", "current", "packages"))
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var answers []json.RawMessage
	if err := json.Unmarshal(output, &answers); err != nil || len(answers) != len(ops) {
		t.Fatalf("oracle output: %v (%d answers for %d ops)", err, len(answers), len(ops))
	}
	for i, op := range ops {
		switch op.Op {
		case "constants":
			var pi struct {
				Limits struct {
					MaxWidth  int `json:"maxWidth"`
					MaxHeight int `json:"maxHeight"`
					MaxBytes  int `json:"maxBytes"`
				} `json:"limits"`
				Inline    []string        `json:"inline"`
				InlineHas map[string]bool `json:"inlineHas"`
			}
			if err := json.Unmarshal(answers[i], &pi); err != nil {
				t.Fatal(err)
			}
			if got := (ImageLimits{MaxWidth: pi.Limits.MaxWidth, MaxHeight: pi.Limits.MaxHeight, MaxBytes: pi.Limits.MaxBytes}); DEFAULT_IMAGE_LIMITS != got {
				t.Errorf("DEFAULT_IMAGE_LIMITS = %+v, Pi %+v", DEFAULT_IMAGE_LIMITS, got)
			}
			var ours []string
			for mime, inline := range INLINE_IMAGE_TYPES {
				if inline {
					ours = append(ours, mime)
				}
			}
			slices.Sort(ours)
			if !slices.Equal(ours, pi.Inline) {
				t.Errorf("INLINE_IMAGE_TYPES = %v, Pi %v", ours, pi.Inline)
			}
			for _, mime := range probes {
				if INLINE_IMAGE_TYPES[mime] != pi.InlineHas[mime] {
					t.Errorf("INLINE_IMAGE_TYPES[%q] = %v, Pi %v", mime, INLINE_IMAGE_TYPES[mime], pi.InlineHas[mime])
				}
			}
		case "base64Length":
			var pi int64
			if err := json.Unmarshal(answers[i], &pi); err != nil {
				t.Fatal(err)
			}
			if got := Base64Length(op.N); got != pi {
				t.Errorf("Base64Length(%d) = %d, Pi %d", op.N, got, pi)
			}
		case "toBase64":
			var pi string
			if err := json.Unmarshal(answers[i], &pi); err != nil {
				t.Fatal(err)
			}
			data := make([]byte, op.Length)
			for index := range data {
				data[index] = byte(index * op.Step % 256)
			}
			if got := ToBase64(data); got != pi {
				t.Errorf("%s: ToBase64 differs from Pi (%d bytes, step %d)", fmt.Sprint(op.Length), len(got), op.Step)
			}
		}
	}
}
