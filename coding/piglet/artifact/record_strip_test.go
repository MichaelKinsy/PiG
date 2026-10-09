package artifact

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func stripBinaryInput(strip []StripEntry) BinaryInput {
	return BinaryInput{
		Target:     "linux/amd64",
		PigVersion: "0.81.1", PigSourceRevision: "revision", PigSourceDigest: "sha256:" + strings.Repeat("1", 64),
		Builder: "native", BuilderIdentity: "native:revision", Toolchains: map[string]string{"go": "go version go1.26"},
		Artifact:     Artifact{Digest: "sha256:" + strings.Repeat("2", 64), Size: 42, FileName: "pig-review"},
		Verification: Verification{Policy: "basic", Passed: true, Checks: []string{"artifact-sha256", "artifact-version-smoke"}},
		Strip:        strip,
	}
}

// TestBinaryRecordStripIsPartOfIdentityAndDigest pins that the Binary
// record carries the strip list canonically and that changing the list, or
// tampering with it, changes or breaks the digest. A record without a strip
// list keeps the current shape (no strip field).
func TestBinaryRecordStripIsPartOfIdentityAndDigest(t *testing.T) {
	t.Parallel()
	createdAt := time.Date(2026, time.October, 5, 12, 0, 0, 0, time.UTC)
	resolution, err := NewResolutionRecord("review", "1.2.0", createdAt, validResolutionInput(t))
	if err != nil {
		t.Fatal(err)
	}
	build := func(strip []StripEntry) Record {
		t.Helper()
		record, err := NewBinaryRecord("review", "1.2.0", createdAt, resolution, stripBinaryInput(strip))
		if err != nil {
			t.Fatal(err)
		}
		return record
	}
	plain := build(nil)
	grep := build([]StripEntry{{Kind: "tool", ID: "grep", Disposition: "runtime"}})
	both := build([]StripEntry{{Kind: "tool", ID: "grep", Disposition: "runtime"}, {Kind: "command", ID: "/share", Disposition: "runtime"}})
	if plain.Digest == grep.Digest || grep.Digest == both.Digest || plain.Digest == both.Digest {
		t.Fatalf("strip list does not change the Binary digest: %s %s %s", plain.Digest, grep.Digest, both.Digest)
	}
	if both.Binary.Strip[0].Kind != "command" {
		t.Fatalf("strip entries are not canonical: %#v", both.Binary.Strip)
	}
	encoded, err := json.Marshal(plain)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), `"strip"`) {
		t.Fatalf("a Binary without a strip list serializes a strip field: %s", encoded)
	}

	tampered := grep
	binary := *grep.Binary
	binary.Strip = nil
	tampered.Binary = &binary
	if err := ValidateRecord(tampered); err == nil || !strings.Contains(err.Error(), "digest mismatch") {
		t.Fatalf("dropping the strip list: ValidateRecord() error = %v", err)
	}

	if _, err := NewBinaryRecord("review", "1.2.0", createdAt, resolution, stripBinaryInput([]StripEntry{{Kind: "slot", ID: "x", Disposition: "runtime"}})); err == nil || !strings.Contains(err.Error(), "strip kind") {
		t.Fatalf("unknown kind error = %v", err)
	}
	if _, err := NewBinaryRecord("review", "1.2.0", createdAt, resolution, stripBinaryInput([]StripEntry{{Kind: "tool", ID: "grep", Disposition: "linked"}})); err == nil || !strings.Contains(err.Error(), "disposition") {
		t.Fatalf("unsupported disposition error = %v", err)
	}
	// A binary disposition (the Binary does not link the built-in) and an api
	// entry are part of the identity like any other entry.
	runtimeMCP := build([]StripEntry{{Kind: "extension", ID: "mcp", Disposition: "runtime"}})
	binaryMCP := build([]StripEntry{{Kind: "api", ID: "bedrock-converse-stream", Disposition: "binary"}, {Kind: "extension", ID: "mcp", Disposition: "binary"}})
	if runtimeMCP.Digest == binaryMCP.Digest {
		t.Fatal("the disposition does not change the Binary digest")
	}
	if err := ValidateRecord(binaryMCP); err != nil {
		t.Fatalf("binary disposition: ValidateRecord() error = %v", err)
	}
}

// TestBinaryRecordCarriesKeepModeAndTheStripTable pins stripKeep and
// stripTable: canonical order, an empty keep list that survives as [], both
// in the digest, and records that reject unsorted, duplicate, unknown-kind or
// null entries.
func TestBinaryRecordCarriesKeepModeAndTheStripTable(t *testing.T) {
	t.Parallel()
	createdAt := time.Date(2026, time.October, 8, 12, 0, 0, 0, time.UTC)
	resolution, err := NewResolutionRecord("core", "1.0.0", createdAt, validResolutionInput(t))
	if err != nil {
		t.Fatal(err)
	}
	build := func(keep, table []StripIDs) (Record, error) {
		input := stripBinaryInput([]StripEntry{{Kind: "extension", ID: "mcp", Disposition: "binary"}})
		input.StripKeep, input.StripTable = keep, table
		return NewBinaryRecord("core", "1.0.0", createdAt, resolution, input)
	}
	table := []StripIDs{{Kind: "tool", IDs: []string{"read", "bash"}}, {Kind: "extension", IDs: []string{"mcp"}}}
	keep := []StripIDs{{Kind: "tool", IDs: []string{"read"}}, {Kind: "extension", IDs: nil}}
	record, err := build(keep, table)
	if err != nil {
		t.Fatal(err)
	}
	if got := record.Binary.StripKeep; len(got) != 2 || got[0].Kind != "extension" || got[0].IDs == nil || len(got[0].IDs) != 0 {
		t.Fatalf("stripKeep is not canonical: %#v", got)
	}
	if got := record.Binary.StripTable[1].IDs; got[0] != "bash" || got[1] != "read" {
		t.Fatalf("stripTable IDs are not sorted: %#v", got)
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"stripKeep":[{"kind":"extension","ids":[]},{"kind":"tool","ids":["read"]}]`, `"stripTable":[{"kind":"extension","ids":["mcp"]},{"kind":"tool","ids":["bash","read"]}]`} {
		if !strings.Contains(string(encoded), want) {
			t.Fatalf("record JSON lacks %s:\n%s", want, encoded)
		}
	}
	parsed, err := ParseRecord(encoded)
	if err != nil || parsed.Digest != record.Digest {
		t.Fatalf("ParseRecord() = %v, %v", parsed.Digest, err)
	}
	deny, err := build(nil, table)
	if err != nil {
		t.Fatal(err)
	}
	if deny.Digest == record.Digest || strings.Contains(mustJSON(t, deny), `"stripKeep"`) {
		t.Fatalf("stripKeep is not part of the identity, or a deny-mode record writes it: %s", mustJSON(t, deny))
	}

	tampered := record
	binary := *record.Binary
	binary.StripKeep = binary.StripKeep[1:]
	tampered.Binary = &binary
	if err := ValidateRecord(tampered); err == nil || !strings.Contains(err.Error(), "digest mismatch") {
		t.Fatalf("dropping a keep-mode list: ValidateRecord() error = %v", err)
	}
	for name, mutate := range map[string]func(*Binary){
		"unknown kind":  func(b *Binary) { b.StripKeep[0].Kind = "slot" },
		"unsorted kind": func(b *Binary) { b.StripTable[0], b.StripTable[1] = b.StripTable[1], b.StripTable[0] },
		"duplicate ID":  func(b *Binary) { b.StripTable[1].IDs = []string{"read", "read"} },
		"null IDs":      func(b *Binary) { b.StripKeep[0].IDs = nil },
	} {
		binary := *record.Binary
		binary.StripKeep = append([]StripIDs{}, record.Binary.StripKeep...)
		binary.StripTable = append([]StripIDs{}, record.Binary.StripTable...)
		mutate(&binary)
		if err := validateBinary(binary); err == nil {
			t.Errorf("%s: validateBinary accepted %#v", name, binary)
		}
	}
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
