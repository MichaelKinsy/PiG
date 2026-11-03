package output

// pi: packages/chord/src/output.ts (utf8ByteLength, OutputBuffer.push)

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"unicode/utf16"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// wtf8 spells UTF-16 code units as a Go string: a surrogate pair is one code point and a lone surrogate keeps its 3-byte WTF-8 form, as a JavaScript
// string with that unit reaches Go.
func wtf8(units []uint16) string {
	var out []byte
	for i := 0; i < len(units); i++ {
		unit := rune(units[i])
		if unit >= 0xd800 && unit < 0xdc00 && i+1 < len(units) && units[i+1] >= 0xdc00 && units[i+1] < 0xe000 {
			out = append(out, string(utf16.DecodeRune(unit, rune(units[i+1])))...)
			i++
			continue
		}
		if unit >= 0xd800 && unit < 0xe000 {
			out = append(out, 0xe0|byte(unit>>12), 0x80|byte(unit>>6)&0x3f, 0x80|byte(unit)&0x3f)
			continue
		}
		out = append(out, string(unit)...)
	}
	return string(out)
}

// output.ts utf8ByteLength is Node's Buffer.byteLength(content, "utf8"): a lone surrogate counts as U+FFFD, three bytes.
func TestUtf8ByteLengthMatchesPi(t *testing.T) {
	cases := [][]uint16{
		{},
		{'a', 'b', 'c'},
		{0xe9},
		{0x65e5, 0x672c, 0x8a9e},
		{0xd83d, 0xde00},
		{0xd800},
		{0xdc00},
		{'a', 0xd800, 'b'},
		{0xdc00, 0xd800},
		{0xd83d, 0xde00, 0xd83d},
		{0xfeff, 'x'},
		{0, 0x7f, 0x80, 0x7ff, 0x800, 0xffff},
	}
	payload, err := json.Marshal(cases)
	if err != nil {
		t.Fatal(err)
	}
	root := testenv.ModuleRoot(t)
	cmd := exec.CommandContext(t.Context(), "node", "--disable-warning=ExperimentalWarning", "testdata/utf8_byte_length_oracle.mjs", filepath.Join(root, ".upstream", "current", "packages"))
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var want []int
	if err := json.Unmarshal(stdout, &want); err != nil {
		t.Fatal(err)
	}
	if len(want) != len(cases) {
		t.Fatalf("Pi answered %d of %d strings", len(want), len(cases))
	}
	for i, units := range cases {
		if got := Utf8ByteLength(wtf8(units)); got != want[i] {
			t.Errorf("Utf8ByteLength(units %x) = %d, Pi utf8ByteLength = %d", units, got, want[i])
		}
	}
}

// OutputBuffer.push(chunk, skipped?) takes a string or bytes with an optional skipped; the generic Push must answer as Pi does for every program
// of the oracle corpus, which TestOutputBufferMatchesPi drives through the typed methods.
func TestOutputBufferPushMatchesPiThroughTheGenericChunkAndSkippedArguments(t *testing.T) {
	programs := outputPrograms()[:1500]
	payload, err := json.Marshal(programs)
	if err != nil {
		t.Fatal(err)
	}
	root := testenv.ModuleRoot(t)
	cmd := exec.CommandContext(t.Context(), "node", "--disable-warning=ExperimentalWarning", "testdata/output_oracle.mjs", filepath.Join(root, ".upstream", "current", "packages"))
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected [][]outputOutcome
	if err := json.Unmarshal(stdout, &expected); err != nil {
		t.Fatal(err)
	}
	push := func(buffer *OutputBuffer, op outputOp) (accepted bool, err error) {
		var skipped *OutputSkip
		if op.Skipped != nil {
			skipped = &OutputSkip{Bytes: op.Skipped.Bytes, Newlines: op.Skipped.Newlines, EndsWithNewline: op.Skipped.EndsWithNewline}
		}
		if op.Data != nil {
			data, _ := base64.StdEncoding.DecodeString(*op.Data)
			return buffer.Push(data, skipped)
		}
		return buffer.Push(*op.Text, skipped)
	}
	pushes, skips, rejected := 0, 0, 0
	for i, program := range programs {
		buffer := NewOutputBuffer(OutputLimits{MaxBytes: program.Limits.MaxBytes, MaxLines: program.Limits.MaxLines, Retain: program.Limits.Retain})
		for j, op := range program.Ops {
			want := expected[i][j]
			switch op.Op {
			case "end":
				buffer.End()
			case "snapshot":
				got := buffer.Snapshot()
				var wantSnapshot map[string]any
				_ = json.Unmarshal(want.OK, &wantSnapshot)
				gotSnapshot := map[string]any{"text": got.Text, "droppedBytes": float64(got.DroppedBytes), "droppedLines": float64(got.DroppedLines)}
				if !reflect.DeepEqual(gotSnapshot, wantSnapshot) {
					t.Errorf("program %d op %d: snapshot %v, Pi %v", i, j, gotSnapshot, wantSnapshot)
				}
			case "push", "bytes", "skip":
				accepted, gotErr := push(buffer, op)
				if (gotErr == nil) != (want.Err == nil) || (gotErr != nil && gotErr.Error() != *want.Err) {
					t.Errorf("program %d op %d (%s) limits %+v: Push error %v, Pi %v", i, j, op.Op, program.Limits, gotErr, ptrOutputText(want.Err))
					continue
				}
				if gotErr == nil {
					var wantAccepted bool
					if err := json.Unmarshal(want.OK, &wantAccepted); err != nil || accepted != wantAccepted {
						t.Errorf("program %d op %d (%s): Push accepted %v, Pi %s", i, j, op.Op, accepted, want.OK)
					}
				}
				pushes++
				if op.Op == "skip" {
					skips++
					if gotErr != nil {
						rejected++
					}
				}
			}
			if buffer.StoredBytes() != want.Stored && op.Op != "bound" && op.Op != "sanitize" {
				t.Errorf("program %d op %d (%s): stored %d bytes, Pi %d", i, j, op.Op, buffer.StoredBytes(), want.Stored)
			}
		}
	}
	if pushes < 1000 || skips < 100 || rejected < 20 {
		t.Errorf("the corpus is one-sided: %d pushes, %d skips, %d rejected skips", pushes, skips, rejected)
	}
}
