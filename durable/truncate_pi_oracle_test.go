package durable

// pi: packages/durable/src/truncate.ts (truncateHead, truncateHeadOf, formatSize, utf8ByteLength)

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
	"github.com/MichaelKinsy/PiG/internal/truncate"
)

type truncateOp struct {
	Op       string `json:"op"`
	Content  string `json:"content"`
	Prefix   string `json:"prefix"`
	Lines    int    `json:"lines"`
	Bytes    int    `json:"bytes"`
	MaxLines *int   `json:"maxLines,omitempty"`
	MaxBytes *int   `json:"maxBytes,omitempty"`
}

func truncateOps() []truncateOp {
	random := rand.New(rand.NewSource(20261114))
	runes := []string{"a", "b", " ", "é", "ß", "€", "日", "😀", "𝄞", "\n", "\n", "\r", "\t"}
	text := func() string {
		var b strings.Builder
		for range random.Intn(40) {
			b.WriteString(runes[random.Intn(len(runes))])
		}
		return b.String()
	}
	limit := func(low, high int) *int {
		if random.Intn(5) == 0 {
			return nil
		}
		return new(low + random.Intn(high-low+1))
	}
	var ops []truncateOp
	for range 4000 {
		content := text()
		maxLines, maxBytes := limit(-1, 8), limit(-2, 70)
		if content == "" || content == "\n" {
			// Not mirrored: an empty text past a negative limit makes Pi call Buffer.byteLength(undefined) (a TypeError); Go truncates it to nothing. No caller passes a negative limit.
			maxLines, maxBytes = limit(0, 8), limit(0, 70)
		}
		ops = append(ops,
			truncateOp{Op: "head", Content: content, MaxLines: maxLines, MaxBytes: maxBytes},
			truncateOp{Op: "bytes", Content: content},
			// The prefix is the whole text, which satisfies the documented precondition.
			truncateOp{Op: "headOf", Prefix: content, Lines: truncate.CountLines(content), Bytes: Utf8ByteLength(content), MaxLines: maxLines, MaxBytes: maxBytes},
			truncateOp{Op: "size", Bytes: []int{0, 1, 1023, 1024, 1025, 1536, 10239, 10240, 1048575, 1048576, 1048576 * 3 / 2, random.Intn(5_000_000)}[random.Intn(12)]},
		)
	}
	return ops
}

// TestTruncateMatchesPi runs Pi's truncate.ts and the Go port over seeded texts with line and byte limits (default, zero, negative), multi-byte and astral characters, CR and trailing newlines.
func TestTruncateMatchesPi(t *testing.T) {
	ops := truncateOps()
	payload, err := json.Marshal(ops)
	if err != nil {
		t.Fatal(err)
	}
	root := testenv.ModuleRoot(t)
	cmd := exec.CommandContext(t.Context(), "node", "--disable-warning=ExperimentalWarning", "testdata/truncate_oracle.mjs", filepath.Join(root, ".upstream", "current", "packages"))
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []struct {
		OK  json.RawMessage `json:"ok"`
		Err *string         `json:"err"`
	}
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	differing, errors := 0, 0
	for index, op := range ops {
		var got any
		var gotErr string
		func() {
			defer func() {
				if recovered := recover(); recovered != nil {
					gotErr = fmt.Sprint(recovered)
				}
			}()
			options := TruncationOptions{MaxLines: op.MaxLines, MaxBytes: op.MaxBytes}
			switch op.Op {
			case "head":
				got = TruncateHead(op.Content, options)
			case "headOf":
				got = TruncateHeadOf(op.Prefix, TruncationTotals{Lines: op.Lines, Bytes: op.Bytes}, options)
			case "size":
				got = FormatSize(op.Bytes)
			default:
				got = Utf8ByteLength(op.Content)
			}
		}()
		want := expected[index]
		if want.Err != nil {
			errors++
		}
		var gotJSON, wantJSON any
		if gotErr == "" {
			encoded, _ := json.Marshal(got)
			_ = json.Unmarshal(encoded, &gotJSON)
		}
		if want.OK != nil {
			_ = json.Unmarshal(want.OK, &wantJSON)
		}
		if (gotErr != "") != (want.Err != nil) || !reflect.DeepEqual(gotJSON, wantJSON) {
			differing++
			if differing <= 6 {
				encoded, _ := json.Marshal(op)
				t.Errorf("op %d %s\n got  %v %q\n want %s %v", index, encoded, gotJSON, gotErr, want.OK, want.Err)
			}
		}
	}
	t.Logf("%d ops, %d Pi errors, %d differing", len(ops), errors, differing)
	if differing > 0 {
		t.Fatalf("%d ops differ from Pi", differing)
	}
}
