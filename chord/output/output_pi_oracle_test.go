package output

// pi: packages/chord/src/output.ts (sanitizeOutput, boundOutput, OutputBuffer)

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/rand"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

type outputSkipped struct {
	Bytes           int  `json:"bytes"`
	Newlines        int  `json:"newlines"`
	EndsWithNewline bool `json:"endsWithNewline"`
}

type outputLimitsJSON struct {
	MaxBytes int    `json:"maxBytes"`
	MaxLines int    `json:"maxLines"`
	Retain   string `json:"retain"`
}

type outputOp struct {
	Op      string         `json:"op"`
	Text    *string        `json:"text,omitempty"`
	Data    *string        `json:"data,omitempty"`
	Skipped *outputSkipped `json:"skipped,omitempty"`
}

type outputProgram struct {
	Limits outputLimitsJSON `json:"limits"`
	Ops    []outputOp       `json:"ops"`
}

var outputPieces = []string{"a", "bc", "\n", "line\n", "two\nlines\n", "é", "😀", "\ufeff", "\x01x", "\ufff9y\ufffb", "\r\n", "日本語", "\t", "\x7f", "\u2028", " ", "\x00", "\x1f", "tail without newline"}

func outputText(random *rand.Rand) string {
	var builder strings.Builder
	for range random.Intn(6) {
		if random.Intn(25) == 0 {
			builder.WriteString(strings.Repeat("0123456789", 1+random.Intn(8)))
			continue
		}
		builder.WriteString(outputPieces[random.Intn(len(outputPieces))])
	}
	return builder.String()
}

// outputBytes spells a chunk as bytes that may split a multi-byte character, start with a byte-order mark or hold invalid UTF-8.
func outputBytes(random *rand.Rand) []byte {
	var out []byte
	switch random.Intn(5) {
	case 0:
		out = []byte{0xef, 0xbb, 0xbf}
	case 1:
		out = []byte{0xf0, 0x9f}
	case 2:
		out = []byte{0x98, 0x80, 0xe3}
	}
	out = append(out, outputText(random)...)
	if random.Intn(4) == 0 {
		out = append(out, []byte{0xff, 0xc3}[random.Intn(2):]...)
	}
	if random.Intn(4) == 0 && len(out) > 1 {
		out = out[:len(out)-1-random.Intn(2)%len(out)]
	}
	return out
}

func outputPrograms() []outputProgram {
	random := rand.New(rand.NewSource(20261023))
	programs := make([]outputProgram, 0, 4000)
	for range 4000 {
		program := outputProgram{Limits: outputLimitsJSON{
			MaxBytes: []int{0, 1, 2, 3, 5, 8, 10, 24, 40, 200}[random.Intn(10)],
			MaxLines: []int{0, 1, 2, 3, 5, 10}[random.Intn(6)],
			Retain:   []string{"head", "tail"}[random.Intn(2)],
		}}
		for range 1 + random.Intn(14) {
			text := outputText(random)
			op := outputOp{}
			switch random.Intn(10) {
			case 0, 1, 2:
				op.Op, op.Text = "push", &text
			case 3, 4, 5:
				data := base64.StdEncoding.EncodeToString(outputBytes(random))
				op.Op, op.Data = "bytes", &data
			case 6:
				op.Op = "skip"
				op.Skipped = &outputSkipped{Bytes: random.Intn(300), Newlines: random.Intn(12), EndsWithNewline: random.Intn(2) == 0}
				if random.Intn(2) == 0 {
					op.Text = &text
				} else {
					data := base64.StdEncoding.EncodeToString(outputBytes(random))
					op.Data = &data
				}
			case 7:
				op.Op = "end"
			case 8:
				op.Op = "snapshot"
			default:
				if random.Intn(2) == 0 {
					op.Op, op.Text = "bound", &text
				} else {
					op.Op, op.Text = "sanitize", &text
				}
			}
			program.Ops = append(program.Ops, op)
		}
		program.Ops = append(program.Ops, outputOp{Op: "snapshot"})
		programs = append(programs, program)
	}
	return programs
}

type outputOutcome struct {
	OK     json.RawMessage `json:"ok"`
	Err    *string         `json:"err"`
	Stored int             `json:"stored"`
}

func TestOutputBufferMatchesPi(t *testing.T) {
	programs := outputPrograms()
	payload, err := json.Marshal(programs)
	if err != nil {
		t.Fatal(err)
	}
	root := testenv.ModuleRoot(t)
	cmd := exec.CommandContext(t.Context(), "node", "--disable-warning=ExperimentalWarning", "testdata/output_oracle.mjs", filepath.Join(root, ".upstream", "current", "packages"))
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected [][]outputOutcome
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	canonical := func(value any) any {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		var out any
		if err := json.Unmarshal(encoded, &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	differing, snapshots, skipsAccepted, rejected := 0, 0, 0, 0
	for i, program := range programs {
		limits := OutputLimits{MaxBytes: program.Limits.MaxBytes, MaxLines: program.Limits.MaxLines, Retain: program.Limits.Retain}
		buffer := NewOutputBuffer(limits)
		for j, op := range program.Ops {
			var got any
			var gotErr error
			switch op.Op {
			case "push":
				got = buffer.PushString(*op.Text)
			case "bytes":
				data, _ := base64.StdEncoding.DecodeString(*op.Data)
				got = buffer.PushBytes(data)
			case "skip":
				skipped := OutputSkip{Bytes: op.Skipped.Bytes, Newlines: op.Skipped.Newlines, EndsWithNewline: op.Skipped.EndsWithNewline}
				if op.Data != nil {
					data, _ := base64.StdEncoding.DecodeString(*op.Data)
					got, gotErr = buffer.PushBytesSkipping(data, skipped)
				} else {
					got, gotErr = buffer.PushStringSkipping(*op.Text, skipped)
				}
			case "end":
				buffer.End()
			case "snapshot":
				got = buffer.Snapshot()
			case "bound":
				got = BoundOutput(*op.Text, limits)
			case "sanitize":
				got = SanitizeOutput(*op.Text)
			}
			want := expected[i][j]
			same := (gotErr == nil) == (want.Err == nil) && buffer.StoredBytes() == want.Stored
			if same && gotErr != nil {
				same = gotErr.Error() == *want.Err
			}
			if same && gotErr == nil {
				var wantValue any
				_ = json.Unmarshal(want.OK, &wantValue)
				gotValue := canonical(got)
				if m, ok := gotValue.(map[string]any); ok {
					// The Go slice and snapshot types name their fields in Go case.
					renamed := map[string]any{}
					for key, value := range m {
						renamed[strings.ToLower(key[:1])+key[1:]] = value
					}
					gotValue = renamed
				}
				same = reflect.DeepEqual(gotValue, wantValue)
			}
			switch op.Op {
			case "snapshot":
				snapshots++
			case "skip":
				if want.Err == nil {
					skipsAccepted++
				} else {
					rejected++
				}
			}
			if !same {
				differing++
				if differing <= 6 {
					t.Errorf("program %d op %d (%s) limits %+v\n  ops %s\n  Pi  %s %v stored %d\n  Pig %v %v stored %d", i, j, op.Op, program.Limits, describeOutputOps(program.Ops[:j+1]), want.OK, ptrOutputText(want.Err), want.Stored, got, gotErr, buffer.StoredBytes())
				}
			}
		}
	}
	if differing > 0 {
		t.Errorf("%d operations differ from Pi", differing)
	}
	t.Logf("%d programs, %d snapshots, %d skips accepted, %d rejected", len(programs), snapshots, skipsAccepted, rejected)
	if skipsAccepted < 100 || rejected < 100 {
		t.Errorf("skips are one-sided: %d accepted, %d rejected", skipsAccepted, rejected)
	}
}

func ptrOutputText(s *string) string {
	if s == nil {
		return "<nil>"
	}
	return *s
}

func describeOutputOps(ops []outputOp) string {
	var parts []string
	for _, op := range ops {
		text := ""
		switch {
		case op.Text != nil:
			text = fmt.Sprintf("%q", *op.Text)
		case op.Data != nil:
			data, _ := base64.StdEncoding.DecodeString(*op.Data)
			text = fmt.Sprintf("% x", data)
		}
		if op.Skipped != nil {
			text += fmt.Sprintf(" skipped%+v", *op.Skipped)
		}
		parts = append(parts, op.Op+"("+text+")")
	}
	return strings.Join(parts, " ")
}
