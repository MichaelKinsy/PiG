package protocol

// pi: packages/protocol/src/framing.ts

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/rand"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

type framingOp struct {
	Push   *string `json:"push,omitempty"`
	Encode *string `json:"encode,omitempty"`
	End    bool    `json:"end,omitempty"`
}

type framingProbe struct {
	Max *float64    `json:"max"`
	Ops []framingOp `json:"ops"`
}

type framingStep struct {
	Frames []string `json:"frames"`
	Error  string   `json:"error,omitempty"`
}

type framingResult struct {
	Ctor  string        `json:"ctor"`
	Steps []framingStep `json:"steps"`
}

func hexOf(b []byte) *string { s := hex.EncodeToString(b); return &s }

// framingProbes build streams of frames that are cut into chunks at random offsets: payloads of every size class (empty, one byte, the decoder's
// 64 KiB block boundary), a limit that the header overruns, a stream truncated at a header or payload byte, and calls after the decoder has ended or
// failed. The seed is fixed, so the probes are the same on every run.
func framingProbes() []framingProbe {
	random := rand.New(rand.NewSource(20260926))
	payload := func(n int) []byte {
		b := make([]byte, n)
		random.Read(b)
		return b
	}
	frameOf := func(p []byte) []byte {
		var header [4]byte
		binary.BigEndian.PutUint32(header[:], uint32(len(p)))
		return append(header[:], p...)
	}
	sizes := []int{0, 1, 2, 3, 4, 5, 255, 256, 1000, 65535, 65536, 65537, 131072, 140000}
	limit := func(v float64) *float64 { return &v }
	probes := []framingProbe{
		{Max: limit(-1)}, {Max: limit(1.5)}, {Max: limit(4294967296)}, {Max: limit(4294967295)}, {Max: limit(0)}, {Max: nil},
		{Ops: []framingOp{{Encode: hexOf(nil)}, {Encode: hexOf([]byte{1, 2, 3})}, {Encode: hexOf(payload(70000))}}},
	}
	for range 300 {
		var stream []byte
		for range 1 + random.Intn(5) {
			stream = append(stream, frameOf(payload(sizes[random.Intn(len(sizes))]))...)
		}
		probe := framingProbe{}
		switch random.Intn(6) {
		case 0:
			probe.Max = limit(float64(random.Intn(70000)))
		case 1:
			probe.Max = limit(0)
		}
		switch random.Intn(5) {
		case 0: // truncated
			stream = stream[:random.Intn(len(stream)+1)]
		case 1: // a header that overruns the limit
			stream = append(stream, 0xff, 0xff, 0xff, 0xff)
		}
		for len(stream) > 0 {
			n := 1 + random.Intn(min(len(stream), 90000))
			if random.Intn(3) == 0 {
				n = 1 + random.Intn(min(len(stream), 6))
			}
			probe.Ops = append(probe.Ops, framingOp{Push: hexOf(stream[:n])})
			stream = stream[n:]
		}
		switch random.Intn(3) {
		case 0:
			probe.Ops = append(probe.Ops, framingOp{End: true}, framingOp{End: true}, framingOp{Push: hexOf([]byte{1})})
		case 1:
			probe.Ops = append(probe.Ops, framingOp{End: true})
		}
		probes = append(probes, probe)
	}
	return probes
}

func runFramingProbe(probe framingProbe) framingResult {
	decoder, err := NewFrameDecoder(FrameDecoderOptions{MaxFrameLength: probe.Max})
	if err != nil {
		return framingResult{Ctor: errorName(err) + ": " + err.Error(), Steps: []framingStep{}}
	}
	result := framingResult{Steps: []framingStep{}}
	for _, op := range probe.Ops {
		step := framingStep{Frames: []string{}}
		var frames [][]byte
		var err error
		switch {
		case op.Push != nil:
			chunk, _ := hex.DecodeString(*op.Push)
			frames, err = decoder.Push(chunk)
		case op.Encode != nil:
			payload, _ := hex.DecodeString(*op.Encode)
			var frame []byte
			if frame, err = EncodeFrame(payload); err == nil {
				frames = [][]byte{frame}
			}
		default:
			err = decoder.End()
		}
		if err != nil {
			step.Error = errorName(err) + ": " + err.Error()
		}
		for _, f := range frames {
			step.Frames = append(step.Frames, hex.EncodeToString(f))
		}
		result.Steps = append(result.Steps, step)
	}
	return result
}

func errorName(err error) string {
	var frameError *FrameError
	var rangeError *RangeError
	switch {
	case errors.As(err, &frameError):
		return "FrameError"
	case errors.As(err, &rangeError):
		return "RangeError"
	}
	return "Error"
}

// framing.ts encodeFrame and FrameDecoder run in Node from the pinned source against the same probes: the frames each push yields, every error
// name and message, the decoder's failed and ended states, and the maxFrameLength validation must match Pi's.
func TestFrameDecoderMatchesPiOnSeededStreams(t *testing.T) {
	probes := framingProbes()
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(testenv.ModuleRoot(t), ".upstream", "current", "packages", "protocol", "src", "framing.ts")
	cmd := exec.CommandContext(t.Context(), "node", "testdata/framing.mjs", source)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []framingResult
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	failures := 0
	for i, probe := range probes {
		got := runFramingProbe(probe)
		want := expected[i]
		if want.Steps == nil {
			want.Steps = []framingStep{}
		}
		for s := range want.Steps {
			if want.Steps[s].Frames == nil {
				want.Steps[s].Frames = []string{}
			}
		}
		if !reflect.DeepEqual(got, want) {
			if failures++; failures <= 3 {
				t.Errorf("probe %d (max %v, %d ops) differs from Pi:\n  Pig %+v\n  Pi  %+v", i, probe.Max, len(probe.Ops), summarize(got), summarize(want))
			}
		}
	}
	if failures > 3 {
		t.Errorf("%d of %d probes differ from Pi", failures, len(probes))
	}
}

// summarize shortens payloads so a failure message stays readable.
func summarize(r framingResult) framingResult {
	out := framingResult{Ctor: r.Ctor}
	for _, s := range r.Steps {
		short := framingStep{Error: s.Error}
		for _, f := range s.Frames {
			if len(f) > 24 {
				f = f[:24] + "…"
			}
			short.Frames = append(short.Frames, f)
		}
		out.Steps = append(out.Steps, short)
	}
	return out
}
