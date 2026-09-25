package closure

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"

	"github.com/MichaelKinsy/PiG/coding"
)

func TestRenderPortMapProjectsProvisionalRows(t *testing.T) {
	hash := HashBytes([]byte("report"))
	graph, err := Build([]Record{
		&Snapshot{Kind: KindSnapshot, ID: "snapshot:report", UpstreamCommit: strings.Repeat("a", 40), TargetCommit: strings.Repeat("b", 40), ToolchainHash: hash, EnvironmentHash: hash},
		&Pin{Kind: KindPin, ID: "pin:report", SnapshotID: "snapshot:report", Repository: "pig", Commit: strings.Repeat("b", 40), Path: "PORT_MAP.md", SemanticID: "denominator:port-map", StartLine: 1, EndLine: 10, QuoteHash: hash},
		rowOrderFact(t, "snapshot:report", "pin:report", PortMapDataset, "rows", []string{"packages/a.ts", "packages/b.ts"}),
		portMapFact(t, "packages/a.ts", "a.go", "✅", "pin:report", hash),
		portMapFact(t, "packages/b.ts", "b.go", "⬜", "pin:report", hash),
		&ProvisionalClaim{Kind: KindProvisionalClaim, ID: "provisional:denominator:port-map:a", SnapshotID: "snapshot:report", SubjectID: "packages/a.ts", SourcePinID: "pin:report", Status: "✅"},
	})
	if err != nil {
		t.Fatalf("Build(): %v", err)
	}
	have, err := RenderReport(graph, PortMapDataset)
	if err != nil {
		t.Fatalf("RenderReport(): %v", err)
	}
	want := "| upstream | pig | status | proven | waived | provisional | open | contradicted | reason |\n" +
		"|---|---|---|---:|---:|---:|---:|---:|---|\n" +
		"| `packages/a.ts` | `a.go` | ✅ | 0 | 0 | 1 | 0 | 0 | imported status ✅ |\n" +
		"| `packages/b.ts` | `b.go` | ⬜ | 0 | 0 | 0 | 1 | 0 | source row has no imported status claim |\n"
	if string(have) != want {
		t.Fatalf("RenderReport() =\n%s\nwant\n%s", have, want)
	}
}

func TestReadReportRebuildIsDeterministic(t *testing.T) {
	hash := HashBytes([]byte("denominator-store"))
	graph, err := Build([]Record{
		&Snapshot{Kind: KindSnapshot, ID: "snapshot:report", UpstreamCommit: strings.Repeat("a", 40), TargetCommit: strings.Repeat("b", 40), ToolchainHash: hash, EnvironmentHash: hash},
		&Pin{Kind: KindPin, ID: "pin:report", SnapshotID: "snapshot:report", Repository: "pig", Commit: strings.Repeat("b", 40), Path: "PORT_MAP.md", SemanticID: "denominator:port-map", StartLine: 1, EndLine: 2, QuoteHash: hash},
		rowOrderFact(t, "snapshot:report", "pin:report", PortMapDataset, "rows", []string{"packages/example.ts"}),
		portMapFact(t, "packages/example.ts", "example.go", "✅", "pin:report", hash),
		&ProvisionalClaim{Kind: KindProvisionalClaim, ID: "provisional:denominator:port-map:example", SnapshotID: "snapshot:report", SubjectID: "packages/example.ts", SourcePinID: "pin:report", Status: "✅"},
	})
	if err != nil {
		t.Fatalf("Build(): %v", err)
	}
	databasePath := t.TempDir() + "/closure.db"
	if err := RebuildStore(t.Context(), databasePath, graph); err != nil {
		t.Fatalf("RebuildStore(first): %v", err)
	}
	first, err := ReadReport(t.Context(), databasePath, PortMapDataset)
	if err != nil {
		t.Fatalf("ReadReport(first): %v", err)
	}
	if err := RebuildStore(t.Context(), databasePath, graph); err != nil {
		t.Fatalf("RebuildStore(second): %v", err)
	}
	second, err := ReadReport(t.Context(), databasePath, PortMapDataset)
	if err != nil {
		t.Fatalf("ReadReport(second): %v", err)
	}
	if string(first) != string(second) {
		t.Fatalf("report changed after rebuild:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}

func TestReadJSONReportRebuildIsDeterministic(t *testing.T) {
	hash := HashBytes([]byte("denominator-json-store"))
	graph, err := Build([]Record{
		&Snapshot{Kind: KindSnapshot, ID: "snapshot:denominator-json-store", UpstreamCommit: strings.Repeat("a", 40), TargetCommit: strings.Repeat("b", 40), ToolchainHash: hash, EnvironmentHash: hash},
		&Pin{Kind: KindPin, ID: "pin:denominator-json-store", SnapshotID: "snapshot:denominator-json-store", Repository: "pig", Commit: strings.Repeat("b", 40), Path: "mapping.json", SemanticID: "denominator:semantic-mapping", StartLine: 1, EndLine: 2, QuoteHash: hash},
		&Fact{Kind: KindFact, ID: "fact:denominator-json-store:metadata", SnapshotID: "snapshot:denominator-json-store", FactType: "denominator:semantic-mapping", SubjectID: "<metadata>", Resolution: "resolved", PinIDs: []string{"pin:denominator-json-store"}, Value: json.RawMessage(`{"upstreamVersion":"0.84.0"}`)},
		rowOrderFact(t, "snapshot:denominator-json-store", "pin:denominator-json-store", SemanticMappingDataset, "mappings", []string{"pkg:ai/.#Example"}),
		&Fact{Kind: KindFact, ID: "fact:denominator-json-store:row", SnapshotID: "snapshot:denominator-json-store", FactType: "denominator:semantic-mapping", SubjectID: "pkg:ai/.#Example", Resolution: "observed", PinIDs: []string{"pin:denominator-json-store"}, Value: json.RawMessage(`{"id":"pkg:ai/.#Example","disposition":"pending"}`)},
	})
	if err != nil {
		t.Fatalf("Build(): %v", err)
	}
	databasePath := t.TempDir() + "/closure.db"
	if err := RebuildStore(t.Context(), databasePath, graph); err != nil {
		t.Fatalf("RebuildStore(first): %v", err)
	}
	first, err := ReadReport(t.Context(), databasePath, SemanticMappingDataset)
	if err != nil {
		t.Fatalf("ReadReport(first): %v", err)
	}
	if err := RebuildStore(t.Context(), databasePath, graph); err != nil {
		t.Fatalf("RebuildStore(second): %v", err)
	}
	second, err := ReadReport(t.Context(), databasePath, SemanticMappingDataset)
	if err != nil {
		t.Fatalf("ReadReport(second): %v", err)
	}
	if string(first) != string(second) {
		t.Fatalf("JSON report changed after rebuild:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}

func TestRenderCoverageProjectsPortMapClaim(t *testing.T) {
	hash := HashBytes([]byte("denominator-coverage"))
	graph, err := Build([]Record{
		&Snapshot{Kind: KindSnapshot, ID: "snapshot:denominator-coverage", UpstreamCommit: strings.Repeat("a", 40), TargetCommit: strings.Repeat("b", 40), ToolchainHash: hash, EnvironmentHash: hash},
		&Pin{Kind: KindPin, ID: "pin:denominator-coverage", SnapshotID: "snapshot:denominator-coverage", Repository: "pig", Commit: strings.Repeat("b", 40), Path: "parity/coverage.md", SemanticID: "denominator:coverage-row", StartLine: 1, EndLine: 10, QuoteHash: hash},
		rowOrderFact(t, "snapshot:denominator-coverage", "pin:denominator-coverage", CoverageDataset, "rows", []string{"packages/example.ts"}),
		&Fact{Kind: KindFact, ID: "fact:denominator:coverage-row:example", SnapshotID: "snapshot:denominator-coverage", FactType: "denominator:coverage-row", SubjectID: "packages/example.ts", Resolution: "observed", PinIDs: []string{"pin:denominator-coverage"}, Value: json.RawMessage(`{"fields":["` + "`packages/example.ts`" + `","✅","1 (scenario)","1 (scenario)","1 pass"]}`)},
		&ProvisionalClaim{Kind: KindProvisionalClaim, ID: "provisional:denominator:port-map:example", SnapshotID: "snapshot:denominator-coverage", SubjectID: "packages/example.ts", SourcePinID: "pin:denominator-coverage", Status: "✅"},
	})
	if err != nil {
		t.Fatalf("Build(): %v", err)
	}
	have, err := RenderReport(graph, CoverageDataset)
	if err != nil {
		t.Fatalf("RenderReport(): %v", err)
	}
	want := "| upstream | port | scenarios | behavioral | last run | proven | waived | provisional | open | contradicted | reason |\n" +
		"|---|---|---|---|---|---:|---:|---:|---:|---:|---|\n" +
		"| `packages/example.ts` | ✅ | 1 (scenario) | 1 (scenario) | 1 pass | 0 | 0 | 1 | 0 | 0 | imported status ✅ |\n"
	if string(have) != want {
		t.Fatalf("RenderReport() =\n%s\nwant\n%s", have, want)
	}
}

func TestRenderSemanticMappingPreservesRowsAndProjectsState(t *testing.T) {
	hash := HashBytes([]byte("denominator-semantic-mapping"))
	graph, err := Build([]Record{
		&Snapshot{Kind: KindSnapshot, ID: "snapshot:semantic-mapping", UpstreamCommit: strings.Repeat("a", 40), TargetCommit: strings.Repeat("b", 40), ToolchainHash: hash, EnvironmentHash: hash},
		&Pin{Kind: KindPin, ID: "pin:semantic-mapping", SnapshotID: "snapshot:semantic-mapping", Repository: "pig", Commit: strings.Repeat("b", 40), Path: "parity/interfaces/mapping-v0.84.0.json", SemanticID: "denominator:semantic-mapping", StartLine: 1, EndLine: 10, QuoteHash: hash},
		&Fact{Kind: KindFact, ID: "fact:denominator:semantic-mapping:metadata", SnapshotID: "snapshot:semantic-mapping", FactType: "denominator:semantic-mapping", SubjectID: "<metadata>", Resolution: "resolved", PinIDs: []string{"pin:semantic-mapping"}, Value: json.RawMessage(`{"upstreamVersion":"0.84.0"}`)},
		rowOrderFact(t, "snapshot:semantic-mapping", "pin:semantic-mapping", SemanticMappingDataset, "mappings", []string{"pkg:ai/.#Foo"}),
		&Fact{Kind: KindFact, ID: "fact:denominator:semantic-mapping:foo", SnapshotID: "snapshot:semantic-mapping", FactType: "denominator:semantic-mapping", SubjectID: "pkg:ai/.#Foo", Resolution: "observed", PinIDs: []string{"pin:semantic-mapping"}, Value: json.RawMessage(`{"id":"pkg:ai/.#Foo","disposition":"ported","upstreamShapeHash":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","evidence":[]}`)},
		&ProvisionalClaim{Kind: KindProvisionalClaim, ID: "provisional:denominator:semantic-mapping:foo", SnapshotID: "snapshot:semantic-mapping", SubjectID: "pkg:ai/.#Foo", SourcePinID: "pin:semantic-mapping", Status: "ported"},
	})
	if err != nil {
		t.Fatalf("Build(): %v", err)
	}
	have, err := RenderReport(graph, SemanticMappingDataset)
	if err != nil {
		t.Fatalf("RenderReport(): %v", err)
	}
	want := "{\n" +
		"  \"upstreamVersion\": \"0.84.0\",\n" +
		"  \"mappings\": [\n" +
		"    {\n" +
		"      \"id\": \"pkg:ai/.#Foo\",\n" +
		"      \"disposition\": \"ported\",\n" +
		"      \"upstreamShapeHash\": \"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\",\n" +
		"      \"evidence\": [],\n" +
		"      \"proven\": 0,\n" +
		"      \"waived\": 0,\n" +
		"      \"provisional\": 1,\n" +
		"      \"open\": 0,\n" +
		"      \"contradicted\": 0,\n" +
		"      \"reason\": \"imported status ported\"\n" +
		"    }\n" +
		"  ]\n" +
		"}\n"
	if string(have) != want {
		t.Fatalf("RenderReport() =\n%s\nwant\n%s", have, want)
	}
}

func TestRenderSemanticMappingPreservesSourceOrder(t *testing.T) {
	hash := HashBytes([]byte("denominator-semantic-mapping-order"))
	graph, err := Build([]Record{
		&Snapshot{Kind: KindSnapshot, ID: "snapshot:semantic-mapping-order", UpstreamCommit: strings.Repeat("a", 40), TargetCommit: strings.Repeat("b", 40), ToolchainHash: hash, EnvironmentHash: hash},
		&Pin{Kind: KindPin, ID: "pin:semantic-mapping-order", SnapshotID: "snapshot:semantic-mapping-order", Repository: "pig", Commit: strings.Repeat("b", 40), Path: "mapping.json", SemanticID: "denominator:semantic-mapping", StartLine: 1, EndLine: 3, QuoteHash: hash},
		&Fact{Kind: KindFact, ID: "fact:denominator:semantic-mapping-order:metadata", SnapshotID: "snapshot:semantic-mapping-order", FactType: "denominator:semantic-mapping", SubjectID: "<metadata>", Resolution: "resolved", PinIDs: []string{"pin:semantic-mapping-order"}, Value: json.RawMessage(`{"upstreamVersion":"0.84.0"}`)},
		rowOrderFact(t, "snapshot:semantic-mapping-order", "pin:semantic-mapping-order", SemanticMappingDataset, "mappings", []string{"pkg:ai/.#Zulu", "pkg:ai/.#Alpha"}),
		&Fact{Kind: KindFact, ID: "fact:denominator:semantic-mapping-order:zulu", SnapshotID: "snapshot:semantic-mapping-order", FactType: "denominator:semantic-mapping", SubjectID: "pkg:ai/.#Zulu", Resolution: "observed", PinIDs: []string{"pin:semantic-mapping-order"}, Value: json.RawMessage(`{"id":"pkg:ai/.#Zulu","disposition":"pending"}`)},
		&Fact{Kind: KindFact, ID: "fact:denominator:semantic-mapping-order:alpha", SnapshotID: "snapshot:semantic-mapping-order", FactType: "denominator:semantic-mapping", SubjectID: "pkg:ai/.#Alpha", Resolution: "observed", PinIDs: []string{"pin:semantic-mapping-order"}, Value: json.RawMessage(`{"id":"pkg:ai/.#Alpha","disposition":"pending"}`)},
	})
	if err != nil {
		t.Fatalf("Build(): %v", err)
	}
	report, err := RenderReport(graph, SemanticMappingDataset)
	if err != nil {
		t.Fatalf("RenderReport(): %v", err)
	}
	zulu := strings.Index(string(report), `"id": "pkg:ai/.#Zulu"`)
	alpha := strings.Index(string(report), `"id": "pkg:ai/.#Alpha"`)
	if zulu < 0 || alpha < 0 || zulu >= alpha {
		t.Fatalf("source order was not preserved:\n%s", report)
	}
}

func TestRenderSemanticDeltaPreservesRowsAndProjectsState(t *testing.T) {
	hash := HashBytes([]byte("denominator-semantic-delta"))
	graph, err := Build([]Record{
		&Snapshot{Kind: KindSnapshot, ID: "snapshot:semantic-delta", UpstreamCommit: strings.Repeat("a", 40), TargetCommit: strings.Repeat("b", 40), ToolchainHash: hash, EnvironmentHash: hash},
		&Pin{Kind: KindPin, ID: "pin:semantic-delta", SnapshotID: "snapshot:semantic-delta", Repository: "pig", Commit: strings.Repeat("b", 40), Path: "parity/interfaces/delta-v0.83.0-v0.84.0.json", SemanticID: "denominator:semantic-delta", StartLine: 1, EndLine: 10, QuoteHash: hash},
		&Fact{Kind: KindFact, ID: "fact:denominator:semantic-delta:metadata", SnapshotID: "snapshot:semantic-delta", FactType: "denominator:semantic-delta", SubjectID: "<metadata>", Resolution: "resolved", PinIDs: []string{"pin:semantic-delta"}, Value: json.RawMessage(`{"from":"0.83.0","to":"0.84.0"}`)},
		rowOrderFact(t, "snapshot:semantic-delta", "pin:semantic-delta", SemanticDeltaDataset, "changes", []string{"pkg:ai/.#Foo"}),
		&Fact{Kind: KindFact, ID: "fact:denominator:semantic-delta:foo", SnapshotID: "snapshot:semantic-delta", FactType: "denominator:semantic-delta", SubjectID: "pkg:ai/.#Foo", Resolution: "resolved", PinIDs: []string{"pin:semantic-delta"}, Value: json.RawMessage(`{"id":"pkg:ai/.#Foo","change":"added","after_shape_hash":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","disposition":"pending","evidence":[],"rationale":""}`)},
	})
	if err != nil {
		t.Fatalf("Build(): %v", err)
	}
	have, err := RenderReport(graph, SemanticDeltaDataset)
	if err != nil {
		t.Fatalf("RenderReport(): %v", err)
	}
	want := "{\n" +
		"  \"from\": \"0.83.0\",\n" +
		"  \"to\": \"0.84.0\",\n" +
		"  \"changes\": [\n" +
		"    {\n" +
		"      \"id\": \"pkg:ai/.#Foo\",\n" +
		"      \"change\": \"added\",\n" +
		"      \"after_shape_hash\": \"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\",\n" +
		"      \"disposition\": \"pending\",\n" +
		"      \"evidence\": [],\n" +
		"      \"rationale\": \"\",\n" +
		"      \"proven\": 0,\n" +
		"      \"waived\": 0,\n" +
		"      \"provisional\": 0,\n" +
		"      \"open\": 1,\n" +
		"      \"contradicted\": 0,\n" +
		"      \"reason\": \"source row has no imported status claim\"\n" +
		"    }\n" +
		"  ]\n" +
		"}\n"
	if string(have) != want {
		t.Fatalf("RenderReport() =\n%s\nwant\n%s", have, want)
	}
}

func TestRenderInputRenderMappingPreservesBothSections(t *testing.T) {
	hash := HashBytes([]byte("denominator-input-render-mapping"))
	graph, err := Build([]Record{
		&Snapshot{Kind: KindSnapshot, ID: "snapshot:input-render", UpstreamCommit: strings.Repeat("a", 40), TargetCommit: strings.Repeat("b", 40), ToolchainHash: hash, EnvironmentHash: hash},
		&Pin{Kind: KindPin, ID: "pin:input-render", SnapshotID: "snapshot:input-render", Repository: "pig", Commit: strings.Repeat("b", 40), Path: "parity/interfaces/behavior-input-mapping-v0.84.0.json", SemanticID: "denominator:behavior-input-mapping", StartLine: 1, EndLine: 10, QuoteHash: hash},
		&Fact{Kind: KindFact, ID: "fact:denominator:input-render:metadata", SnapshotID: "snapshot:input-render", FactType: "denominator:behavior-input-mapping", SubjectID: "<metadata>", Resolution: "resolved", PinIDs: []string{"pin:input-render"}, Value: json.RawMessage(`{"upstreamVersion":"0.84.0"}`)},
		rowOrderFact(t, "snapshot:input-render", "pin:input-render", InputRenderMappingDataset, "mappings", []string{"input:example"}),
		rowOrderFact(t, "snapshot:input-render", "pin:input-render", InputRenderMappingDataset, "renderMappings", []string{"render:example"}),
		&Fact{Kind: KindFact, ID: "fact:denominator:input-render:input", SnapshotID: "snapshot:input-render", FactType: "denominator:behavior-input-mapping", SubjectID: "input:example", Resolution: "observed", PinIDs: []string{"pin:input-render"}, Value: json.RawMessage(`{"id":"input:example","ownerFamily":"selectors","disposition":"partial","pigTargets":["tui/example.go"],"evidence":[],"contracts":[]}`)},
		&Fact{Kind: KindFact, ID: "fact:denominator:input-render:render", SnapshotID: "snapshot:input-render", FactType: "denominator:behavior-input-mapping", SubjectID: "render:example", Resolution: "observed", PinIDs: []string{"pin:input-render"}, Value: json.RawMessage(`{"id":"render:example","ownerFamily":"selectors","disposition":"pending","pigTargets":[],"evidence":[],"contracts":[]}`)},
		&ProvisionalClaim{Kind: KindProvisionalClaim, ID: "provisional:denominator:behavior-input-mapping:input", SnapshotID: "snapshot:input-render", SubjectID: "input:example", SourcePinID: "pin:input-render", Status: "partial"},
	})
	if err != nil {
		t.Fatalf("Build(): %v", err)
	}
	have, err := RenderReport(graph, InputRenderMappingDataset)
	if err != nil {
		t.Fatalf("RenderReport(): %v", err)
	}
	want := "{\n" +
		"  \"upstreamVersion\": \"0.84.0\",\n" +
		"  \"mappings\": [\n" +
		"    {\n" +
		"      \"id\": \"input:example\",\n" +
		"      \"ownerFamily\": \"selectors\",\n" +
		"      \"disposition\": \"partial\",\n" +
		"      \"pigTargets\": [\n" +
		"        \"tui/example.go\"\n" +
		"      ],\n" +
		"      \"evidence\": [],\n" +
		"      \"contracts\": [],\n" +
		"      \"proven\": 0,\n" +
		"      \"waived\": 0,\n" +
		"      \"provisional\": 1,\n" +
		"      \"open\": 0,\n" +
		"      \"contradicted\": 0,\n" +
		"      \"reason\": \"imported status partial\"\n" +
		"    }\n" +
		"  ],\n" +
		"  \"renderMappings\": [\n" +
		"    {\n" +
		"      \"id\": \"render:example\",\n" +
		"      \"ownerFamily\": \"selectors\",\n" +
		"      \"disposition\": \"pending\",\n" +
		"      \"pigTargets\": [],\n" +
		"      \"evidence\": [],\n" +
		"      \"contracts\": [],\n" +
		"      \"proven\": 0,\n" +
		"      \"waived\": 0,\n" +
		"      \"provisional\": 0,\n" +
		"      \"open\": 1,\n" +
		"      \"contradicted\": 0,\n" +
		"      \"reason\": \"source row has no imported status claim\"\n" +
		"    }\n" +
		"  ]\n" +
		"}\n"
	if string(have) != want {
		t.Fatalf("RenderReport() =\n%s\nwant\n%s", have, want)
	}
}

func TestRenderAsyncContractsPreservesRowsAndProjectsState(t *testing.T) {
	hash := HashBytes([]byte("denominator-async-contract"))
	graph, err := Build([]Record{
		&Snapshot{Kind: KindSnapshot, ID: "snapshot:async-contract", UpstreamCommit: strings.Repeat("a", 40), TargetCommit: strings.Repeat("b", 40), ToolchainHash: hash, EnvironmentHash: hash},
		&Pin{Kind: KindPin, ID: "pin:async-contract", SnapshotID: "snapshot:async-contract", Repository: "pig", Commit: strings.Repeat("b", 40), Path: "parity/async-contracts.toml", SemanticID: "denominator:async-contract", StartLine: 1, EndLine: 10, QuoteHash: hash},
		&Fact{Kind: KindFact, ID: "fact:denominator:async-contract:metadata", SnapshotID: "snapshot:async-contract", FactType: "denominator:async-contract", SubjectID: "<metadata>", Resolution: "resolved", PinIDs: []string{"pin:async-contract"}, Value: json.RawMessage(`{"version":"0.84.0"}`)},
		rowOrderFact(t, "snapshot:async-contract", "pin:async-contract", AsyncContractDataset, "files", []string{"packages/example.ts"}),
		&Fact{Kind: KindFact, ID: "fact:denominator:async-contract:row", SnapshotID: "snapshot:async-contract", FactType: "denominator:async-contract", SubjectID: "packages/example.ts", Resolution: "observed", PinIDs: []string{"pin:async-contract"}, Value: json.RawMessage(`{"contracts":["awaited"],"disposition":"ported","evidence":["example_test.go"],"path":"packages/example.ts","rationale":"reviewed"}`)},
		&ProvisionalClaim{Kind: KindProvisionalClaim, ID: "provisional:denominator:async-contract:row", SnapshotID: "snapshot:async-contract", SubjectID: "packages/example.ts", SourcePinID: "pin:async-contract", Status: "ported"},
	})
	if err != nil {
		t.Fatalf("Build(): %v", err)
	}
	have, err := RenderReport(graph, AsyncContractDataset)
	if err != nil {
		t.Fatalf("RenderReport(): %v", err)
	}
	want := "version = \"0.84.0\"\n\n" +
		"[[files]]\n" +
		"path = \"packages/example.ts\"\n" +
		"disposition = \"ported\"\n" +
		"contracts = [\"awaited\"]\n" +
		"evidence = [\"example_test.go\"]\n" +
		"rationale = \"reviewed\"\n" +
		"proven = 0\nwaived = 0\nprovisional = 1\nopen = 0\ncontradicted = 0\n" +
		"reason = \"imported status ported\"\n"
	if string(have) != want {
		t.Fatalf("RenderReport() =\n%s\nwant\n%s", have, want)
	}
}

func TestRenderChangedSourcePreservesRowsAndProjectsState(t *testing.T) {
	hash := HashBytes([]byte("denominator-upstream-sync"))
	graph, err := Build([]Record{
		&Snapshot{Kind: KindSnapshot, ID: "snapshot:upstream-sync", UpstreamCommit: strings.Repeat("a", 40), TargetCommit: strings.Repeat("b", 40), ToolchainHash: hash, EnvironmentHash: hash},
		&Pin{Kind: KindPin, ID: "pin:upstream-sync", SnapshotID: "snapshot:upstream-sync", Repository: "pig", Commit: strings.Repeat("b", 40), Path: "parity/upstream-sync/v0.84.0.toml", SemanticID: "denominator:upstream-sync", StartLine: 1, EndLine: 10, QuoteHash: hash},
		&Fact{Kind: KindFact, ID: "fact:denominator:upstream-sync:metadata", SnapshotID: "snapshot:upstream-sync", FactType: "denominator:upstream-sync", SubjectID: "<metadata>", Resolution: "resolved", PinIDs: []string{"pin:upstream-sync"}, Value: json.RawMessage(`{"from":"0.83.0","to":"0.84.0"}`)},
		rowOrderFact(t, "snapshot:upstream-sync", "pin:upstream-sync", ChangedSourceDataset, "files", []string{"packages/example.ts"}),
		&Fact{Kind: KindFact, ID: "fact:denominator:upstream-sync:row", SnapshotID: "snapshot:upstream-sync", FactType: "denominator:upstream-sync", SubjectID: "packages/example.ts", Resolution: "observed", PinIDs: []string{"pin:upstream-sync"}, Value: json.RawMessage(`{"change":"modified","disposition":"pending","evidence":[],"path":"packages/example.ts","rationale":""}`)},
	})
	if err != nil {
		t.Fatalf("Build(): %v", err)
	}
	have, err := RenderReport(graph, ChangedSourceDataset)
	if err != nil {
		t.Fatalf("RenderReport(): %v", err)
	}
	want := "from = \"0.83.0\"\nto = \"0.84.0\"\n\n" +
		"[[files]]\n" +
		"path = \"packages/example.ts\"\n" +
		"change = \"modified\"\n" +
		"disposition = \"pending\"\n" +
		"evidence = []\n" +
		"rationale = \"\"\n" +
		"proven = 0\nwaived = 0\nprovisional = 0\nopen = 1\ncontradicted = 0\n" +
		"reason = \"source row has no imported status claim\"\n"
	if string(have) != want {
		t.Fatalf("RenderReport() =\n%s\nwant\n%s", have, want)
	}
}

func TestRenderBehaviorContractsPreservesNestedSource(t *testing.T) {
	hash := HashBytes([]byte("denominator-behavior-contract"))
	graph, err := Build([]Record{
		&Snapshot{Kind: KindSnapshot, ID: "snapshot:behavior-contract", UpstreamCommit: strings.Repeat("a", 40), TargetCommit: strings.Repeat("b", 40), ToolchainHash: hash, EnvironmentHash: hash},
		&Pin{Kind: KindPin, ID: "pin:behavior-contract", SnapshotID: "snapshot:behavior-contract", Repository: "pig", Commit: strings.Repeat("b", 40), Path: "parity/behavior-contracts.toml", SemanticID: "denominator:behavior-contract", StartLine: 1, EndLine: 20, QuoteHash: hash},
		&Fact{Kind: KindFact, ID: "fact:denominator:behavior-contract:metadata", SnapshotID: "snapshot:behavior-contract", FactType: "denominator:behavior-contract", SubjectID: "<metadata>", Resolution: "resolved", PinIDs: []string{"pin:behavior-contract"}, Value: json.RawMessage(`{"upstream_version":"0.84.0"}`)},
		rowOrderFact(t, "snapshot:behavior-contract", "pin:behavior-contract", BehaviorContractDataset, "contract", []string{"example/wrap"}),
		&Fact{Kind: KindFact, ID: "fact:denominator:behavior-contract:row", SnapshotID: "snapshot:behavior-contract", FactType: "denominator:behavior-contract", SubjectID: "example/wrap", Resolution: "observed", PinIDs: []string{"pin:behavior-contract"}, Value: json.RawMessage(`{"claim":"wraps at both boundaries","evidence":["example_test.go"],"family":"selectors","id":"example/wrap","kind":"boundary-transition","pig_targets":["tui/example.go"],"status":"ported","upstream":{"end":"end marker","keybindings":["tui.select.up"],"path":"packages/example.ts","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","start":"\tstart marker"},"upstream_id":"input:example"}`)},
		&ProvisionalClaim{Kind: KindProvisionalClaim, ID: "provisional:denominator:behavior-contract:row", SnapshotID: "snapshot:behavior-contract", SubjectID: "example/wrap", SourcePinID: "pin:behavior-contract", Status: "ported"},
	})
	if err != nil {
		t.Fatalf("Build(): %v", err)
	}
	have, err := RenderReport(graph, BehaviorContractDataset)
	if err != nil {
		t.Fatalf("RenderReport(): %v", err)
	}
	want := "upstream_version = \"0.84.0\"\n\n" +
		"[[contract]]\n" +
		"id = \"example/wrap\"\n" +
		"upstream_id = \"input:example\"\n" +
		"family = \"selectors\"\n" +
		"kind = \"boundary-transition\"\n" +
		"claim = \"wraps at both boundaries\"\n" +
		"status = \"ported\"\n" +
		"pig_targets = [\"tui/example.go\"]\n" +
		"evidence = [\"example_test.go\"]\n" +
		"proven = 0\nwaived = 0\nprovisional = 1\nopen = 0\ncontradicted = 0\n" +
		"reason = \"imported status ported\"\n\n" +
		"[contract.upstream]\n" +
		"path = \"packages/example.ts\"\n" +
		"start = \"\\tstart marker\"\n" +
		"end = \"end marker\"\n" +
		"sha256 = \"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\"\n" +
		"keybindings = [\"tui.select.up\"]\n"
	if string(have) != want {
		t.Fatalf("RenderReport() =\n%s\nwant\n%s", have, want)
	}
}

func TestRenderFormatVersionsPreservesRows(t *testing.T) {
	hash := HashBytes([]byte("denominator-format-version"))
	graph, err := Build([]Record{
		&Snapshot{Kind: KindSnapshot, ID: "snapshot:format-version", UpstreamCommit: strings.Repeat("a", 40), TargetCommit: strings.Repeat("b", 40), ToolchainHash: hash, EnvironmentHash: hash},
		&Pin{Kind: KindPin, ID: "pin:format-version", SnapshotID: "snapshot:format-version", Repository: "pig", Commit: strings.Repeat("b", 40), Path: "parity/format-versions.toml", SemanticID: "denominator:format-version", StartLine: 1, EndLine: 10, QuoteHash: hash},
		rowOrderFact(t, "snapshot:format-version", "pin:format-version", FormatOwnershipDataset, "fields", []string{"example.go#Config.Version:json:version"}),
		&Fact{Kind: KindFact, ID: "fact:denominator:format-version:row", SnapshotID: "snapshot:format-version", FactType: "denominator:format-version", SubjectID: "example.go#Config.Version:json:version", Resolution: "resolved", PinIDs: []string{"pin:format-version"}, Value: json.RawMessage(`{"classification":"external","field":"Version","id":"example.go#Config.Version:json:version","owner":"Config","path":"example.go","rationale":"external protocol","wire_name":"version"}`)},
	})
	if err != nil {
		t.Fatalf("Build(): %v", err)
	}
	have, err := RenderReport(graph, FormatOwnershipDataset)
	if err != nil {
		t.Fatalf("RenderReport(): %v", err)
	}
	want := "[[fields]]\n" +
		"id = \"example.go#Config.Version:json:version\"\n" +
		"path = \"example.go\"\n" +
		"owner = \"Config\"\n" +
		"field = \"Version\"\n" +
		"wire_name = \"version\"\n" +
		"classification = \"external\"\n" +
		"rationale = \"external protocol\"\n" +
		"proven = 0\nwaived = 0\nprovisional = 0\nopen = 1\ncontradicted = 0\n" +
		"reason = \"source row has no imported status claim\"\n"
	if string(have) != want {
		t.Fatalf("RenderReport() =\n%s\nwant\n%s", have, want)
	}
}

func TestRenderFamilyCoverageDerivesScenarioQuality(t *testing.T) {
	hash := HashBytes([]byte("family-coverage"))
	graph, err := Build([]Record{
		&Snapshot{Kind: KindSnapshot, ID: "snapshot:family-coverage", UpstreamCommit: strings.Repeat("a", 40), TargetCommit: strings.Repeat("b", 40), ToolchainHash: hash, EnvironmentHash: hash},
		&Pin{Kind: KindPin, ID: "pin:family-coverage", SnapshotID: "snapshot:family-coverage", Repository: "pig", Commit: strings.Repeat("b", 40), Path: "parity/scenarios/selectors", SemanticID: "denominator:scenario", StartLine: 1, EndLine: 10, QuoteHash: hash},
		scenarioFact("snapshot:family-coverage", "pin:family-coverage", "parity/scenarios/selectors/01-behavior.toml", `{"description":"behavior","covers":["packages/a.ts"],"tags":["hermetic"]}`),
		scenarioFact("snapshot:family-coverage", "pin:family-coverage", "parity/scenarios/selectors/02-boot.toml", `{"description":"boot-only: starts","covers":["packages/b.ts"],"tags":[]}`),
		scenarioFact("snapshot:family-coverage", "pin:family-coverage", "parity/scenarios/selectors/03-registration.toml", `{"description":"registration","covers":["packages/ai/src/api-registry.ts"],"tags":["registration-only"]}`),
		scenarioFact("snapshot:family-coverage", "pin:family-coverage", "parity/scenarios/04-top.toml", `{"description":"top behavior","covers":["packages/top.ts"],"tags":[]}`),
		scenarioFact("snapshot:family-coverage", "pin:family-coverage", "parity/scenarios/selectors/04-deferred.toml", `{"description":"later","covers":["packages/c.ts"],"tags":["deferred"]}`),
	})
	if err != nil {
		t.Fatalf("Build(): %v", err)
	}
	have, err := RenderReport(graph, FamilyCoverageDataset)
	if err != nil {
		t.Fatalf("RenderReport(): %v", err)
	}
	want := "| family | scenarios | behavioral | boot-only | weak | deferred | upstream behavioral covered | last run | proven | waived | provisional | open | contradicted | reason |\n" +
		"|---|---:|---:|---:|---:|---:|---:|---|---:|---:|---:|---:|---:|---|\n" +
		"| `_top` | 1 | 1 | 0 | 0 | 0 | 1 | not recorded | 0 | 0 | 0 | 1 | 0 | no passing scenario run recorded |\n" +
		"| `selectors` | 3 | 1 | 1 | 1 | 1 | 2 | not recorded | 0 | 0 | 0 | 3 | 0 | no passing scenario run recorded |\n"
	if string(have) != want {
		t.Fatalf("RenderReport() =\n%s\nwant\n%s", have, want)
	}
	if _, err := scenarioFamily("parity/scenarios/bad|family/example.toml"); err == nil || !strings.Contains(err.Error(), "report delimiter") {
		t.Fatalf("scenario family delimiter error = %v", err)
	}
}

func TestRenderScenarioQualityProjectsEachScenario(t *testing.T) {
	hash := HashBytes([]byte("scenario-quality"))
	graph, err := Build([]Record{
		&Snapshot{Kind: KindSnapshot, ID: "snapshot:scenario-quality", UpstreamCommit: strings.Repeat("a", 40), TargetCommit: strings.Repeat("b", 40), ToolchainHash: hash, EnvironmentHash: hash},
		&Pin{Kind: KindPin, ID: "pin:scenario-quality", SnapshotID: "snapshot:scenario-quality", Repository: "pig", Commit: strings.Repeat("b", 40), Path: "parity/scenarios", SemanticID: "denominator:scenario", StartLine: 1, EndLine: 10, QuoteHash: hash},
		scenarioFact("snapshot:scenario-quality", "pin:scenario-quality", "parity/scenarios/02-top.toml", `{"description":"boot-only: starts","covers":["packages/top.ts"],"tags":[]}`),
		scenarioFact("snapshot:scenario-quality", "pin:scenario-quality", "parity/scenarios/selectors/01-behavior.toml", `{"description":"behavior","covers":["packages/a.ts","packages/b.ts"],"tags":[]}`),
		scenarioFact("snapshot:scenario-quality", "pin:scenario-quality", "parity/scenarios/selectors/03-deferred.toml", `{"description":"later","covers":[],"tags":["deferred"]}`),
	})
	if err != nil {
		t.Fatalf("Build(): %v", err)
	}
	have, err := RenderReport(graph, ScenarioQualityDataset)
	if err != nil {
		t.Fatalf("RenderReport(): %v", err)
	}
	want := "| scenario | family | quality | covers | proven | waived | provisional | open | contradicted | reason |\n" +
		"|---|---|---|---:|---:|---:|---:|---:|---:|---|\n" +
		"| `parity/scenarios/02-top.toml` | `_top` | boot-only | 1 | 0 | 0 | 0 | 1 | 0 | no passing scenario run recorded |\n" +
		"| `parity/scenarios/selectors/01-behavior.toml` | `selectors` | behavioral | 2 | 0 | 0 | 0 | 1 | 0 | no passing scenario run recorded |\n" +
		"| `parity/scenarios/selectors/03-deferred.toml` | `selectors` | deferred | 0 | 0 | 0 | 0 | 1 | 0 | no passing scenario run recorded |\n"
	if string(have) != want {
		t.Fatalf("RenderReport() =\n%s\nwant\n%s", have, want)
	}
}

func TestRenderDivergenceDashboardKeepsApprovalProvisional(t *testing.T) {
	hash := HashBytes([]byte("divergence-dashboard"))
	graph, err := Build([]Record{
		&Snapshot{Kind: KindSnapshot, ID: "snapshot:divergence-dashboard", UpstreamCommit: strings.Repeat("a", 40), TargetCommit: strings.Repeat("b", 40), ToolchainHash: hash, EnvironmentHash: hash},
		&Pin{Kind: KindPin, ID: "pin:divergence-dashboard", SnapshotID: "snapshot:divergence-dashboard", Repository: "pig", Commit: strings.Repeat("b", 40), Path: "DIVERGENCES.md", SemanticID: "denominator:divergence", StartLine: 1, EndLine: 10, QuoteHash: hash},
		&Fact{Kind: KindFact, ID: "fact:denominator:divergence:D10", SnapshotID: "snapshot:divergence-dashboard", FactType: "denominator:divergence", SubjectID: "D10", Resolution: "observed", PinIDs: []string{"pin:divergence-dashboard"}, Value: json.RawMessage(`{"section":"## D10 open difference\n\nWhat: unresolved."}`)},
		&Fact{Kind: KindFact, ID: "fact:denominator:divergence:D2", SnapshotID: "snapshot:divergence-dashboard", FactType: "denominator:divergence", SubjectID: "D2", Resolution: "observed", PinIDs: []string{"pin:divergence-dashboard"}, Value: json.RawMessage(`{"section":"## D2 approved difference\n\nSCRUTINIZED:approved"}`)},
		&ProvisionalClaim{Kind: KindProvisionalClaim, ID: "provisional:denominator:divergence:D2", SnapshotID: "snapshot:divergence-dashboard", SubjectID: "D2", SourcePinID: "pin:divergence-dashboard", Status: "approved"},
	})
	if err != nil {
		t.Fatalf("Build(): %v", err)
	}
	have, err := RenderReport(graph, DivergenceDashboardDataset)
	if err != nil {
		t.Fatalf("RenderReport(): %v", err)
	}
	want := "| divergence | title | imported scrutiny | proven | waived | provisional | open | contradicted | reason |\n" +
		"|---|---|---|---:|---:|---:|---:|---:|---|\n" +
		"| D2 | approved difference | approved | 0 | 0 | 1 | 0 | 0 | imported status approved |\n" +
		"| D10 | open difference | unapproved | 0 | 0 | 0 | 1 | 0 | imported divergence has no imported approval claim |\n"
	if string(have) != want {
		t.Fatalf("RenderReport() =\n%s\nwant\n%s", have, want)
	}
}

func TestRenderFoundationDashboardUsesDerivedObligationVerdicts(t *testing.T) {
	graph, err := Build(provedFixture(t))
	if err != nil {
		t.Fatalf("Build(): %v", err)
	}
	report, err := RenderReport(graph, FoundationDashboardDataset)
	if err != nil {
		t.Fatalf("RenderReport(): %v", err)
	}
	rows := markdownTableRows(t, report, "| scope | items | proven | waived | provisional | open | contradicted | ready | reason |")
	var obligations []string
	for _, row := range rows {
		if row[0] == "obligations" {
			obligations = row
			break
		}
	}
	want := []string{"obligations", "1", "1", "0", "0", "0", "0", "yes", "derived obligation verdicts"}
	if !reflect.DeepEqual(obligations, want) {
		t.Fatalf("obligation row = %v, want %v", obligations, want)
	}

	reopenedRecords := provedFixture(t)
	for _, record := range reopenedRecords {
		if rule, ok := record.(*Rule); ok && rule.ID == "rule:result" {
			rule.DefinitionHash = HashBytes([]byte("changed-foundation-rule"))
		}
	}
	reopened, err := Build(reopenedRecords)
	if err != nil {
		t.Fatalf("Build(reopened): %v", err)
	}
	reopenedReport, err := RenderReport(reopened, FoundationDashboardDataset)
	if err != nil {
		t.Fatalf("RenderReport(reopened): %v", err)
	}
	for _, row := range markdownTableRows(t, reopenedReport, "| scope | items | proven | waived | provisional | open | contradicted | ready | reason |") {
		if row[0] == "obligations" {
			want = []string{"obligations", "1", "0", "0", "0", "1", "0", "no", "derived obligation verdicts"}
			if !reflect.DeepEqual(row, want) {
				t.Fatalf("reopened obligation row = %v, want %v", row, want)
			}
			return
		}
	}
	t.Fatal("reopened dashboard has no obligation row")
}

func TestRenderReportRejectsUnsupportedAndMalformedRows(t *testing.T) {
	hash := HashBytes([]byte("hash"))
	graph, err := Build([]Record{
		&Snapshot{Kind: KindSnapshot, ID: "snapshot:report", UpstreamCommit: strings.Repeat("a", 40), TargetCommit: strings.Repeat("b", 40), ToolchainHash: hash, EnvironmentHash: hash},
		&Pin{Kind: KindPin, ID: "pin:report", SnapshotID: "snapshot:report", Repository: "pig", Commit: strings.Repeat("b", 40), Path: "PORT_MAP.md", SemanticID: "denominator:port-map", StartLine: 1, EndLine: 10, QuoteHash: hash},
	})
	if err != nil {
		t.Fatalf("Build(): %v", err)
	}
	if _, err := RenderReport(graph, "family"); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("unsupported dataset error = %v", err)
	}
	if _, err := RenderReport(graph, PortMapDataset); err == nil || !strings.Contains(err.Error(), "row order") {
		t.Fatalf("missing row order error = %v", err)
	}
	badValue, err := json.Marshal(map[string]any{"fields": []string{"`packages/b.ts`", "`b.go`"}})
	if err != nil {
		t.Fatal(err)
	}
	badGraph, err := Build([]Record{
		&Snapshot{Kind: KindSnapshot, ID: "snapshot:report", UpstreamCommit: strings.Repeat("a", 40), TargetCommit: strings.Repeat("b", 40), ToolchainHash: hash, EnvironmentHash: hash},
		&Pin{Kind: KindPin, ID: "pin:report", SnapshotID: "snapshot:report", Repository: "pig", Commit: strings.Repeat("b", 40), Path: "PORT_MAP.md", SemanticID: "denominator:port-map", StartLine: 1, EndLine: 10, QuoteHash: hash},
		rowOrderFact(t, "snapshot:report", "pin:report", PortMapDataset, "rows", []string{"packages/b.ts"}),
		&Fact{Kind: KindFact, ID: "fact:denominator:port-map:bad", SnapshotID: "snapshot:report", FactType: "denominator:port-map", SubjectID: "packages/b.ts", Resolution: "observed", PinIDs: []string{"pin:report"}, Value: badValue},
	})
	if err != nil {
		t.Fatalf("Build(bad): %v", err)
	}
	if _, err := RenderReport(badGraph, PortMapDataset); err == nil || !strings.Contains(err.Error(), "invalid fields") {
		t.Fatalf("malformed row error = %v", err)
	}
}

func TestRenderJSONReportRejectsReservedFieldsAndOrphanClaims(t *testing.T) {
	hash := HashBytes([]byte("bad-denominator-json"))
	snapshot := &Snapshot{Kind: KindSnapshot, ID: "snapshot:bad-denominator-json", UpstreamCommit: strings.Repeat("a", 40), TargetCommit: strings.Repeat("b", 40), ToolchainHash: hash, EnvironmentHash: hash}
	pin := &Pin{Kind: KindPin, ID: "pin:bad-denominator-json", SnapshotID: snapshot.ID, Repository: "pig", Commit: strings.Repeat("b", 40), Path: "mapping.json", SemanticID: "denominator:semantic-mapping", StartLine: 1, EndLine: 2, QuoteHash: hash}
	metadata := &Fact{Kind: KindFact, ID: "fact:bad-denominator-json:metadata", SnapshotID: snapshot.ID, FactType: "denominator:semantic-mapping", SubjectID: "<metadata>", Resolution: "resolved", PinIDs: []string{pin.ID}, Value: json.RawMessage(`{"upstreamVersion":"0.84.0"}`)}
	reserved := &Fact{Kind: KindFact, ID: "fact:bad-denominator-json:reserved", SnapshotID: snapshot.ID, FactType: "denominator:semantic-mapping", SubjectID: "pkg:ai/.#Bad", Resolution: "observed", PinIDs: []string{pin.ID}, Value: json.RawMessage(`{"id":"pkg:ai/.#Bad","disposition":"pending","proven":1}`)}
	reservedOrder := rowOrderFact(t, snapshot.ID, pin.ID, SemanticMappingDataset, "mappings", []string{"pkg:ai/.#Bad"})
	graph, err := Build([]Record{snapshot, pin, metadata, reservedOrder, reserved})
	if err != nil {
		t.Fatalf("Build(reserved): %v", err)
	}
	if _, err := RenderReport(graph, SemanticMappingDataset); err == nil || !strings.Contains(err.Error(), "reserved state field") {
		t.Fatalf("reserved field error = %v", err)
	}

	orphan := &ProvisionalClaim{Kind: KindProvisionalClaim, ID: "provisional:denominator:semantic-mapping:orphan", SnapshotID: snapshot.ID, SubjectID: "pkg:ai/.#Orphan", SourcePinID: pin.ID, Status: "ported"}
	orphanOrder := rowOrderFact(t, snapshot.ID, pin.ID, SemanticMappingDataset, "mappings", []string{})
	graph, err = Build([]Record{snapshot, pin, metadata, orphanOrder, orphan})
	if err != nil {
		t.Fatalf("Build(orphan): %v", err)
	}
	if _, err := RenderReport(graph, SemanticMappingDataset); err == nil || !strings.Contains(err.Error(), "has no row") {
		t.Fatalf("orphan claim error = %v", err)
	}
}

func TestStructuredReportsPreserveCurrentAuthorityRows(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	hash := HashBytes([]byte("report-current-authority"))
	records, _, err := ImportCurrentDenominators(root, &Snapshot{
		Kind: KindSnapshot, ID: "snapshot:report-current-authority",
		UpstreamCommit: strings.Repeat("a", 40), TargetCommit: strings.Repeat("b", 40),
		ToolchainHash: hash, EnvironmentHash: hash,
	})
	if err != nil {
		t.Fatalf("ImportCurrentDenominators(): %v", err)
	}
	graph, err := Build(records)
	if err != nil {
		t.Fatalf("Build(): %v", err)
	}
	for _, dataset := range ReportDatasets() {
		output, err := RenderReport(graph, dataset)
		if err != nil {
			t.Fatalf("RenderReport(%s): %v", dataset, err)
		}
		if len(output) == 0 {
			t.Fatalf("RenderReport(%s) returned no output", dataset)
		}
	}
	for _, test := range []struct {
		name     string
		dataset  string
		path     string
		sections []string
		toml     bool
	}{
		{name: "semantic mapping", dataset: SemanticMappingDataset, path: "parity/interfaces/mapping-v" + coding.UpstreamVersion + ".json", sections: []string{"mappings"}},
		{name: "semantic delta", dataset: SemanticDeltaDataset, path: "parity/interfaces/delta-v" + coding.UpstreamReviewedVersion + "-v" + coding.UpstreamVersion + ".json", sections: []string{"changes"}},
		{name: "input and render mapping", dataset: InputRenderMappingDataset, path: "parity/interfaces/behavior-input-mapping-v" + coding.UpstreamVersion + ".json", sections: []string{"mappings", "renderMappings"}},
		{name: "async", dataset: AsyncContractDataset, path: "parity/async-contracts.toml", sections: []string{"files"}, toml: true},
		{name: "changed source", dataset: ChangedSourceDataset, path: "parity/upstream-sync/v" + coding.UpstreamVersion + ".toml", sections: []string{"files"}, toml: true},
		{name: "behavior", dataset: BehaviorContractDataset, path: "parity/behavior-contracts.toml", sections: []string{"contract"}, toml: true},
		{name: "format ownership", dataset: FormatOwnershipDataset, path: "parity/format-versions.toml", sections: []string{"fields"}, toml: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			source, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(test.path)))
			if err != nil {
				t.Fatal(err)
			}
			projected, err := RenderReport(graph, test.dataset)
			if err != nil {
				t.Fatalf("RenderReport(): %v", err)
			}
			sourceDocument := decodeReportDocument(t, source, test.toml)
			projectedDocument := decodeReportDocument(t, projected, test.toml)
			for _, section := range test.sections {
				stripStateFields(t, projectedDocument, section)
			}
			if !reflect.DeepEqual(projectedDocument, sourceDocument) {
				t.Fatalf("projected %s rows differ from %s", test.dataset, test.path)
			}
		})
	}
}

func TestMarkdownReportsPreserveCurrentAuthorityRows(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	hash := HashBytes([]byte("denominator-markdown-current-authority"))
	records, _, err := ImportCurrentDenominators(root, &Snapshot{
		Kind: KindSnapshot, ID: "snapshot:denominator-markdown-current-authority",
		UpstreamCommit: strings.Repeat("a", 40), TargetCommit: strings.Repeat("b", 40),
		ToolchainHash: hash, EnvironmentHash: hash,
	})
	if err != nil {
		t.Fatalf("ImportCurrentDenominators(): %v", err)
	}
	graph, err := Build(records)
	if err != nil {
		t.Fatalf("Build(): %v", err)
	}
	for _, test := range []struct {
		name, dataset, path string
		denominatorFields   int
	}{
		{name: "port map", dataset: PortMapDataset, path: "PORT_MAP.md", denominatorFields: 3},
		{name: "coverage", dataset: CoverageDataset, path: "parity/coverage.md", denominatorFields: 5},
	} {
		t.Run(test.name, func(t *testing.T) {
			current, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(test.path)))
			if err != nil {
				t.Fatal(err)
			}
			projected, err := RenderReport(graph, test.dataset)
			if err != nil {
				t.Fatalf("RenderReport(): %v", err)
			}
			currentRows := packageTableRows(t, current, test.denominatorFields)
			projectedRows := packageTableRows(t, projected, test.denominatorFields)
			if !reflect.DeepEqual(projectedRows, currentRows) {
				t.Fatalf("projected %s rows differ from %s", test.dataset, test.path)
			}
		})
	}
}

func packageTableRows(t *testing.T, data []byte, denominatorFields int) [][]string {
	t.Helper()
	rows := make([][]string, 0)
	for line := range strings.SplitSeq(string(data), "\n") {
		if !strings.HasPrefix(line, "| `packages/") {
			continue
		}
		fields := strings.Split(line, "|")[1:]
		fields = fields[:len(fields)-1]
		if len(fields) < denominatorFields {
			t.Fatalf("source table row has %d fields, want at least %d: %s", len(fields), denominatorFields, line)
		}
		for index := range denominatorFields {
			fields[index] = strings.TrimSpace(fields[index])
		}
		rows = append(rows, fields[:denominatorFields])
	}
	return rows
}

func TestFamilyCoverageReportMatchesCurrentStaticDashboard(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	hash := HashBytes([]byte("family-report-current-authority"))
	records, _, err := ImportCurrentDenominators(root, &Snapshot{
		Kind: KindSnapshot, ID: "snapshot:family-report-current-authority",
		UpstreamCommit: strings.Repeat("a", 40), TargetCommit: strings.Repeat("b", 40),
		ToolchainHash: hash, EnvironmentHash: hash,
	})
	if err != nil {
		t.Fatalf("ImportCurrentDenominators(): %v", err)
	}
	graph, err := Build(records)
	if err != nil {
		t.Fatalf("Build(): %v", err)
	}
	projected, err := RenderReport(graph, FamilyCoverageDataset)
	if err != nil {
		t.Fatalf("RenderReport(): %v", err)
	}
	current, err := os.ReadFile(filepath.Join(root, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	projectedRows := familyDashboardRows(t, projected)
	currentRows := familyDashboardRows(t, current)
	if len(projectedRows) != len(currentRows) {
		t.Fatalf("family row count = %d, current dashboard = %d", len(projectedRows), len(currentRows))
	}
	for index := range currentRows {
		if len(projectedRows[index]) < 7 || len(currentRows[index]) < 7 || !reflect.DeepEqual(projectedRows[index][:7], currentRows[index][:7]) {
			t.Fatalf("family row %d static fields = %v, current dashboard = %v", index, projectedRows[index], currentRows[index])
		}
	}
}

func TestDivergenceDashboardMatchesCurrentHeadingsAndScrutiny(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	hash := HashBytes([]byte("divergence-report-current-authority"))
	records, _, err := ImportCurrentDenominators(root, &Snapshot{
		Kind: KindSnapshot, ID: "snapshot:divergence-report-current-authority",
		UpstreamCommit: strings.Repeat("a", 40), TargetCommit: strings.Repeat("b", 40),
		ToolchainHash: hash, EnvironmentHash: hash,
	})
	if err != nil {
		t.Fatalf("ImportCurrentDenominators(): %v", err)
	}
	graph, err := Build(records)
	if err != nil {
		t.Fatalf("Build(): %v", err)
	}
	report, err := RenderReport(graph, DivergenceDashboardDataset)
	if err != nil {
		t.Fatalf("RenderReport(): %v", err)
	}
	source, err := os.ReadFile(filepath.Join(root, "DIVERGENCES.md"))
	if err != nil {
		t.Fatal(err)
	}
	titles := make(map[string]string)
	approved := make(map[string]bool)
	currentID := ""
	for line := range strings.SplitSeq(string(source), "\n") {
		if strings.HasPrefix(line, "## D") {
			heading := strings.TrimPrefix(line, "## ")
			id, title, ok := strings.Cut(heading, " ")
			if !ok || id == "" || title == "" {
				t.Fatalf("invalid divergence heading %q", line)
			}
			currentID = id
			titles[id] = title
			continue
		}
		if currentID != "" && strings.Contains(line, "SCRUTINIZED:approved") {
			approved[currentID] = true
		}
	}
	rows := markdownTableRows(t, report, "| divergence | title | imported scrutiny |")
	if len(rows) != len(titles) {
		t.Fatalf("divergence rows = %d, headings = %d", len(rows), len(titles))
	}
	provisional, open := 0, 0
	for _, row := range rows {
		if len(row) != 9 || titles[row[0]] != row[1] {
			t.Fatalf("invalid divergence row %v", row)
		}
		wantScrutiny := "unapproved"
		if approved[row[0]] {
			wantScrutiny = "approved"
		}
		if row[2] != wantScrutiny {
			t.Fatalf("divergence %s scrutiny = %s, want %s", row[0], row[2], wantScrutiny)
		}
		if row[5] == "1" {
			provisional++
		}
		if row[6] == "1" {
			open++
		}
	}
	if provisional != activeDivergenceCount(t) || open != 0 {
		t.Fatalf("divergence state counts = provisional %d, open %d", provisional, open)
	}
}

func TestFoundationDashboardAccountsCurrentDenominatorsWithoutCredit(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	hash := HashBytes([]byte("foundation-report-current-authority"))
	records, _, err := ImportCurrentDenominators(root, &Snapshot{
		Kind: KindSnapshot, ID: "snapshot:foundation-report-current-authority",
		UpstreamCommit: strings.Repeat("a", 40), TargetCommit: strings.Repeat("b", 40),
		ToolchainHash: hash, EnvironmentHash: hash,
	})
	if err != nil {
		t.Fatalf("ImportCurrentDenominators(): %v", err)
	}
	graph, err := Build(records)
	if err != nil {
		t.Fatalf("Build(): %v", err)
	}
	report, err := RenderReport(graph, FoundationDashboardDataset)
	if err != nil {
		t.Fatalf("RenderReport(): %v", err)
	}
	expectations, _ := fileDenominators(t, root)
	wantItems := map[string]denominatorExpectation{
		PortMapDataset: expectations["port-map"], CoverageDataset: expectations["coverage-row"],
		DivergenceDashboardDataset: expectations["divergence"],
	}
	for dataset, expectation := range expectations {
		if dataset != "port-map" && dataset != "coverage-row" && dataset != "divergence" {
			wantItems[dataset] = expectation
		}
	}
	rows := markdownTableRows(t, report, "| scope | items | proven | waived | provisional | open | contradicted | ready | reason |")
	if len(rows) != len(wantItems)+1 {
		t.Fatalf("foundation rows = %d, want %d", len(rows), len(wantItems)+1)
	}
	for _, row := range rows {
		if row[0] == "obligations" {
			if !reflect.DeepEqual(row[:8], []string{"obligations", "0", "0", "0", "0", "0", "0", "no"}) {
				t.Fatalf("obligation row = %v", row)
			}
			continue
		}
		expectation, exists := wantItems[row[0]]
		if !exists {
			t.Fatalf("unexpected foundation scope %s", row[0])
		}
		items, provisional := expectation.items, expectation.provisional
		want := []string{row[0], fmt.Sprint(items), "0", "0", fmt.Sprint(provisional), fmt.Sprint(items - provisional), "0", "no"}
		if !reflect.DeepEqual(row[:8], want) {
			t.Fatalf("foundation row = %v, want prefix %v", row, want)
		}
	}
	for _, record := range records {
		claim, ok := record.(*ProvisionalClaim)
		if !ok {
			continue
		}
		duplicate := *claim
		duplicate.ID += ":duplicate"
		duplicateGraph, err := Build(append(records, &duplicate))
		if err != nil {
			t.Fatalf("Build(duplicate claim): %v", err)
		}
		if _, err := RenderReport(duplicateGraph, FoundationDashboardDataset); err == nil || !strings.Contains(err.Error(), "duplicate claim subject") {
			t.Fatalf("duplicate claim error = %v", err)
		}
		return
	}
	t.Fatal("current denominator has no provisional claim fixture")
}

func familyDashboardRows(t *testing.T, data []byte) [][]string {
	t.Helper()
	return markdownTableRows(t, data, "| family | scenarios | behavioral | boot-only | weak | deferred | upstream behavioral covered | last run |")
}

func markdownTableRows(t *testing.T, data []byte, headerPrefix string) [][]string {
	t.Helper()
	lines := strings.Split(string(data), "\n")
	start := -1
	for index, line := range lines {
		if strings.HasPrefix(line, headerPrefix) {
			start = index
			break
		}
	}
	if start < 0 || start+2 >= len(lines) {
		t.Fatalf("table header %q is missing", headerPrefix)
	}
	rows := make([][]string, 0)
	for _, line := range lines[start+2:] {
		if !strings.HasPrefix(line, "|") {
			break
		}
		fields := strings.Split(line, "|")[1:]
		fields = fields[:len(fields)-1]
		for index := range fields {
			fields[index] = strings.TrimSpace(fields[index])
		}
		rows = append(rows, fields)
	}
	return rows
}

func decodeReportDocument(t *testing.T, data []byte, isTOML bool) map[string]any {
	t.Helper()
	var document map[string]any
	if isTOML {
		if _, err := toml.Decode(string(data), &document); err != nil {
			t.Fatal(err)
		}
		normalized, err := json.Marshal(document)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(normalized, &document); err != nil {
			t.Fatal(err)
		}
		return document
	}
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	return document
}

func stripStateFields(t *testing.T, document map[string]any, section string) {
	t.Helper()
	rows, ok := document[section].([]any)
	if !ok {
		t.Fatalf("section %s has type %T", section, document[section])
	}
	for _, raw := range rows {
		row, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("section %s row has type %T", section, raw)
		}
		for _, field := range []string{"proven", "waived", "provisional", "open", "contradicted", "reason"} {
			delete(row, field)
		}
	}
}

func rowOrderFact(t *testing.T, snapshotID, pinID, dataset, listKey string, subjects []string) *Fact {
	t.Helper()
	value, err := json.Marshal(map[string]any{"subjects": subjects})
	if err != nil {
		t.Fatal(err)
	}
	return &Fact{Kind: KindFact, ID: "fact:denominator-order:" + dataset + ":" + listKey, SnapshotID: snapshotID, FactType: "denominator-order:" + dataset, SubjectID: listKey, Resolution: "resolved", PinIDs: []string{pinID}, Value: value}
}

func scenarioFact(snapshotID, pinID, path, value string) *Fact {
	return &Fact{Kind: KindFact, ID: "fact:denominator:scenario:" + strings.ReplaceAll(path, "/", "-"), SnapshotID: snapshotID, FactType: "denominator:scenario", SubjectID: path, Resolution: "observed", PinIDs: []string{pinID}, Value: json.RawMessage(value)}
}

func portMapFact(t *testing.T, subject, target, status, pinID, hash string) *Fact {
	t.Helper()
	value, err := json.Marshal(map[string]any{"fields": []string{"`" + subject + "`", "`" + target + "`", status}})
	if err != nil {
		t.Fatal(err)
	}
	return &Fact{Kind: KindFact, ID: "fact:denominator:port-map:" + strings.ReplaceAll(subject, "/", "-"), SnapshotID: "snapshot:report", FactType: "denominator:port-map", SubjectID: subject, Resolution: "observed", PinIDs: []string{pinID}, Value: value}
}
