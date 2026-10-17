package tools

import (
	"encoding/hex"
	"math/rand"
	"os"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

type accumulatorProbe struct {
	MaxLines   int      `json:"maxLines"`
	MaxBytes   int      `json:"maxBytes"`
	Chunks     []string `json:"chunks"`
	SnapshotAt []bool   `json:"snapshotAt"`
	Persist    bool     `json:"persist"`
	FullMax    int      `json:"fullMax"`
	AppendLate bool     `json:"appendLate"`
}

type accumulatorSnapshot struct {
	Content               string `json:"content"`
	Truncated             bool   `json:"truncated"`
	TruncatedBy           string `json:"truncatedBy"`
	TotalLines            int    `json:"totalLines"`
	TotalBytes            int    `json:"totalBytes"`
	OutputLines           int    `json:"outputLines"`
	OutputBytes           int    `json:"outputBytes"`
	LastLinePartial       bool   `json:"lastLinePartial"`
	FirstLineExceedsLimit bool   `json:"firstLineExceedsLimit"`
	MaxLines              int    `json:"maxLines"`
	MaxBytes              int    `json:"maxBytes"`
	HasPath               bool   `json:"hasPath"`
}

type accumulatorResult struct {
	Snapshots     []accumulatorSnapshot `json:"snapshots"`
	Final         accumulatorSnapshot   `json:"final"`
	FileHex       string                `json:"fileHex"`
	FullContent   string                `json:"fullContent"`
	FullTruncated bool                  `json:"fullTruncated"`
	LateThrew     bool                  `json:"lateThrew"`
}

// OutputAccumulator against Pi's (core/tools/output-accumulator.js): random byte streams cut into chunks anywhere (inside multi-byte characters,
// inside a BOM), with invalid and truncated sequences, CRLF, long lines and tiny limits. Every snapshot taken between appends, the final one,
// the full-output file, readFullOutput and the append-after-finish error are compared. getLastLineBytes is left out: PiG's D153 differs from Pi
// by design when the output ends with a newline. A BOM appears only at the start of the stream: Node's TextDecoder also drops a BOM that follows a
// leading incomplete sequence (e6 | ef bb bf 42 decodes to U+FFFD 42) and two of three adjacent ones; those streams are left out.
func TestOutputAccumulatorMatchesPi(t *testing.T) {
	rng := rand.New(rand.NewSource(131))
	pieces := [][]byte{
		[]byte("a"), []byte("bc"), []byte("hello world"), []byte("\n"), []byte("\n\n"), []byte("\r\n"), []byte("\r"),
		[]byte("é"), []byte("日本語"), []byte("😀"), {0x80}, {0xC0, 0x80}, {0xF5, 0x80}, {0xFF}, {0xE6, 0x97},
		{0xF0, 0x9F, 0x98}, {0xED, 0xA0, 0x80}, {0xC3}, []byte("long line long line long line long line long line"),
	}
	var probes []accumulatorProbe
	for range 700 {
		var stream []byte
		if rng.Intn(4) == 0 {
			stream = []byte{0xEF, 0xBB, 0xBF} // a BOM that starts the stream is dropped, also when the chunks split it
		}
		for range rng.Intn(14) {
			stream = append(stream, pieces[rng.Intn(len(pieces))]...)
		}
		p := accumulatorProbe{
			MaxLines:   []int{1, 2, 3, 5, 2000}[rng.Intn(5)],
			MaxBytes:   []int{1, 2, 3, 7, 16, 40, 100, 51200}[rng.Intn(8)],
			Persist:    rng.Intn(2) == 0,
			FullMax:    []int{1, 2, 3, 5, 10, 33, 100, 1 << 20}[rng.Intn(8)],
			AppendLate: rng.Intn(4) == 0,
		}
		for len(stream) > 0 {
			n := min(len(stream), rng.Intn(9))
			p.Chunks = append(p.Chunks, hex.EncodeToString(stream[:n]))
			p.SnapshotAt = append(p.SnapshotAt, rng.Intn(3) == 0)
			stream = stream[n:]
		}
		probes = append(probes, p)
	}
	var want []accumulatorResult
	pioracle.Run(t, `
const { OutputAccumulator } = await load("pi-coding-agent/core/tools/output-accumulator.js");
const fs = await import("node:fs");
const shape = (s) => ({
	content: s.content, truncated: s.truncation.truncated, truncatedBy: s.truncation.truncatedBy ?? "",
	totalLines: s.truncation.totalLines, totalBytes: s.truncation.totalBytes, outputLines: s.truncation.outputLines, outputBytes: s.truncation.outputBytes,
	lastLinePartial: s.truncation.lastLinePartial, firstLineExceedsLimit: s.truncation.firstLineExceedsLimit,
	maxLines: s.truncation.maxLines, maxBytes: s.truncation.maxBytes, hasPath: s.fullOutputPath !== undefined,
});
const out = [];
for (const p of input) {
	const acc = new OutputAccumulator({ maxLines: p.maxLines, maxBytes: p.maxBytes, tempFilePrefix: "oracle" });
	const snapshots = [];
	(p.chunks ?? []).forEach((chunk, i) => {
		acc.append(Buffer.from(chunk, "hex"));
		if (p.snapshotAt[i]) snapshots.push(shape(acc.snapshot()));
	});
	acc.finish();
	const final = shape(acc.snapshot({ persistIfTruncated: p.persist }));
	await acc.closeTempFile();
	const snap = acc.snapshot();
	const full = await acc.readFullOutput(p.fullMax);
	let fileHex = "";
	if (snap.fullOutputPath !== undefined) { fileHex = fs.readFileSync(snap.fullOutputPath).toString("hex"); fs.rmSync(snap.fullOutputPath, { force: true }); }
	let lateThrew = false;
	if (p.appendLate) { try { acc.append(Buffer.from("x")); } catch { lateThrew = true; } }
	out.push({ snapshots, final, fileHex, fullContent: full.content, fullTruncated: full.truncated, lateThrew });
}
emit(out);
`, probes, &want)
	bad := 0
	for i, p := range probes {
		acc := newOutputAccumulator(p.MaxLines, p.MaxBytes, "oracle")
		var got accumulatorResult
		shape := func(s OutputSnapshot) accumulatorSnapshot {
			tr := s.Truncation
			return accumulatorSnapshot{Content: s.Content, Truncated: tr.Truncated, TruncatedBy: tr.TruncatedBy, TotalLines: tr.TotalLines, TotalBytes: tr.TotalBytes,
				OutputLines: tr.OutputLines, OutputBytes: tr.OutputBytes, LastLinePartial: tr.LastLinePartial, FirstLineExceedsLimit: tr.FirstLineExceedsLimit,
				MaxLines: tr.MaxLines, MaxBytes: tr.MaxBytes, HasPath: s.FullOutputPath != ""}
		}
		for j, chunk := range p.Chunks {
			data, _ := hex.DecodeString(chunk)
			if err := acc.Append(data); err != nil {
				t.Fatal(err)
			}
			if p.SnapshotAt[j] {
				got.Snapshots = append(got.Snapshots, shape(acc.Snapshot(false)))
			}
		}
		acc.Finish()
		got.Final = shape(acc.Snapshot(p.Persist))
		if err := acc.CloseTempFile(); err != nil {
			t.Fatal(err)
		}
		snap := acc.Snapshot(false)
		full, err := acc.ReadFullOutput(p.FullMax)
		if err != nil {
			t.Fatal(err)
		}
		if snap.FullOutputPath != "" {
			data, err := os.ReadFile(snap.FullOutputPath)
			if err != nil {
				t.Fatal(err)
			}
			got.FileHex = hex.EncodeToString(data)
			_ = os.Remove(snap.FullOutputPath)
		}
		got.FullContent, got.FullTruncated = full.Content, full.Truncated
		if p.AppendLate {
			got.LateThrew = acc.Append([]byte("x")) != nil
		}
		w := want[i]
		if !equalAccumulator(got, w) {
			if bad++; bad <= 4 {
				t.Errorf("probe %d %+v:\n got %+v\nwant %+v", i, p, got, w)
			}
		}
	}
	if bad > 4 {
		t.Errorf("%d of %d probes differ", bad, len(probes))
	}
}

func equalAccumulator(a, b accumulatorResult) bool {
	if len(a.Snapshots) != len(b.Snapshots) || a.Final != b.Final || a.FileHex != b.FileHex || a.FullContent != b.FullContent || a.FullTruncated != b.FullTruncated || a.LateThrew != b.LateThrew {
		return false
	}
	for i := range a.Snapshots {
		if a.Snapshots[i] != b.Snapshots[i] {
			return false
		}
	}
	return true
}
