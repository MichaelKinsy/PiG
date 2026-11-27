package ai

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// TestImageCatalogPin binds the image catalog to @earendil-works/pi-ai's IMAGE_MODELS at the pinned version.
func TestImageCatalogPin(t *testing.T) {
	published := loadOracleCatalog(t)
	if got, want := len(GeneratedImageModels), len(published["image"]); got != want {
		t.Fatalf("image catalog size = %d, want the oracle Pi %s image-catalog count %d", got, UpstreamVersionString(), want)
	}
	model, ok := GetImageModel("openrouter", "microsoft/mai-image-2.5-pro")
	if !ok {
		t.Fatal("0.83.0 image model microsoft/mai-image-2.5-pro missing")
	}
	if model.Name != "Microsoft AI: MAI-Image-2.5 Pro" || model.Cost.Input != 5 {
		t.Fatalf("image model = %+v", model)
	}
	if _, ok := GetImageModel("openrouter", "meta/muse-image"); !ok {
		t.Fatal("0.86.1 image model meta/muse-image missing")
	}
	// 0.87.1 added inclusionai/ming-image-0.1-design; the pin 1.2.0+b06c1f2c providers/data/openrouter.json (frozen in
	// automation/pin/frozen) no longer lists it.
	if _, ok := GetImageModel("openrouter", "inclusionai/ming-image-0.1-design"); ok {
		t.Fatal("1.2.0+b06c1f2c image catalog still has inclusionai/ming-image-0.1-design")
	}
}

func TestRegistryHasModels(t *testing.T) {
	want := len(loadOracleCatalog(t)["chat"]) // Count of the oracle Pi's @earendil-works/pi-ai MODELS export.
	if got := len(GeneratedModels); got != want {
		t.Fatalf("catalog size = %d want %d", got, want)
	}
}

func TestGeneratedInputLimitsAreNotAliasedByToModel(t *testing.T) {
	opus, ok := LookupModelExact("anthropic/claude-opus-5-5")
	if !ok || opus.InputLimits == nil || opus.InputLimits.Images == nil || opus.InputLimits.Images.Resize == nil {
		t.Fatalf("Claude Opus 5.5 inputLimits missing: %+v", opus)
	}
	model := opus.ToModel()
	model.InputLimits.MaxRequestBytes = 1
	model.InputLimits.Images.MaxPerRequest = 1
	model.InputLimits.Images.Resize.MaxWidth = 1
	if opus.InputLimits.MaxRequestBytes != 33554432 || opus.InputLimits.Images.MaxPerRequest != 600 || opus.InputLimits.Images.Resize.MaxWidth != 2000 {
		t.Fatalf("ToModel aliased catalog inputLimits: %#v", opus.InputLimits)
	}
}

// Pi's getBuiltinProviders and builtinModels include the published radius
// catalog (RADIUS_MODELS); pi-messages is an implemented runtime API.
func TestRuntimeDiscoveryIncludesRadiusCatalog(t *testing.T) {
	generatedRadius := 0
	for _, model := range GeneratedModels {
		if model.API == APIPiMessages {
			generatedRadius++
		}
	}
	publishedRadius := 0
	for _, model := range loadOracleCatalog(t)["chat"] {
		if model["api"] == "pi-messages" {
			publishedRadius++
		}
	}
	if generatedRadius != publishedRadius || publishedRadius == 0 {
		t.Fatalf("generated pi-messages models = %d, want the oracle Pi %s count %d", generatedRadius, UpstreamVersionString(), publishedRadius)
	}
	if providers := ListProviders(); !slices.Contains(providers, "radius") {
		t.Fatalf("ListProviders() omits radius: %v", providers)
	}
	if runtimeProviders := ListRuntimeProviders(); !slices.Contains(runtimeProviders, "radius") {
		t.Fatalf("ListRuntimeProviders() omits radius: %v", runtimeProviders)
	}
	models := ListModels("radius")
	if len(models) != generatedRadius {
		t.Fatalf("ListModels(radius) = %d models, want %d", len(models), generatedRadius)
	}
	for _, model := range models {
		if model.API != APIPiMessages {
			t.Fatalf("radius model %s API = %q", model.ID, model.API)
		}
	}
	balanced, ok := LookupModelExact("radius/balanced")
	if !ok || balanced.Enabled == nil || !*balanced.Enabled || balanced.Lab == "" || len(balanced.Providers) != 1 {
		t.Fatalf("Radius generated evidence = %+v, %t", balanced, ok)
	}
}

func TestLookupByFullyQualified(t *testing.T) {
	m, ok := LookupModel("openai/gpt-4o")
	if !ok {
		t.Fatal("openai/gpt-4o not found")
	}
	if m.Provider != "openai" {
		t.Fatalf("provider = %q want openai", m.Provider)
	}
	if m.ContextWindow == 0 {
		t.Errorf("gpt-4o context window = 0 (parser regression)")
	}
}

func TestLookupByBareID(t *testing.T) {
	if _, ok := LookupModel("gpt-4o"); !ok {
		t.Fatal("bare 'gpt-4o' must resolve")
	}
}

func TestLookupMiss(t *testing.T) {
	if m, ok := LookupModel("not/a-real-model-xyz"); ok {
		t.Fatalf("expected miss, got %+v", m)
	}
	if _, ok := LookupModel(""); ok {
		t.Fatal("empty spec must miss")
	}
}

// LookupModelExact resolves only the fully-qualified provider/id key and must
// NOT borrow another provider's same-named model the way LookupModel does.
// This is the property model resolution relies on to avoid giving
// github-copilot/gpt-4o openai gpt-4o's capabilities.
func TestLookupModelExact_NoCrossProviderBorrow(t *testing.T) {
	// LookupModel cross-resolves the bare id (openai's gpt-4o)...
	if _, ok := LookupModel("github-copilot/gpt-4o"); !ok {
		t.Fatal("precondition: LookupModel should cross-resolve github-copilot/gpt-4o to openai's")
	}
	// ...but LookupModelExact must not, since copilot has no gpt-4o.
	if _, ok := LookupModelExact("github-copilot/gpt-4o"); ok {
		t.Error("LookupModelExact resolved github-copilot/gpt-4o; copilot has no gpt-4o")
	}
	// A genuine provider/model still resolves.
	if _, ok := LookupModelExact("github-copilot/gpt-6.1-sol"); !ok {
		t.Error("LookupModelExact should resolve github-copilot/gpt-6.1-sol")
	}
	if _, ok := LookupModelExact(""); ok {
		t.Error("empty spec must miss")
	}
}

func TestToCapabilities(t *testing.T) {
	m, ok := LookupModel("openai/gpt-4o")
	if !ok {
		t.Fatal("gpt-4o missing")
	}
	caps := m.ToCapabilities()
	if !caps.SupportsToolUse {
		t.Error("SupportsToolUse should be true")
	}
	if caps.ContextWindow != m.ContextWindow {
		t.Errorf("ContextWindow mismatch %d vs %d", caps.ContextWindow, m.ContextWindow)
	}
	if caps.InputCostPer1M != m.InputCostPerMTokens {
		t.Errorf("InputCost mismatch")
	}
}

func TestGeneratedCatalogThinkingLevelMap(t *testing.T) {
	t.Run("anthropic opus exposes max mapping", func(t *testing.T) {
		m, ok := LookupModel("anthropic/claude-opus-4-6")
		if !ok {
			t.Fatal("anthropic/claude-opus-4-6 missing")
		}
		if m.ThinkingLevelMap == nil {
			t.Fatal("ThinkingLevelMap = nil")
		}
		// 0.81.1 renamed the top Anthropic thinking key from xhigh to max
		// (thinkingLevelMap {max: max}).
		mapped, ok := m.ThinkingLevelMap[ThinkingMax]
		if !ok || mapped == nil || *mapped != "max" {
			t.Fatalf("max mapping = %v, %t; want max", mapped, ok)
		}
		levels := GetSupportedThinkingLevels(m.ToModel())
		if len(levels) == 0 || levels[len(levels)-1] != ThinkingMax {
			t.Fatalf("supported thinking levels = %v, want max enabled", levels)
		}
	})

	t.Run("gpt-5 disables off via explicit nil mapping", func(t *testing.T) {
		m, ok := LookupModel("openai/gpt-5")
		if !ok {
			t.Fatal("openai/gpt-5 missing")
		}
		mapped, ok := m.ThinkingLevelMap[ThinkingOff]
		if !ok || mapped != nil {
			t.Fatalf("off mapping = %v, %t; want explicit nil", mapped, ok)
		}
		levels := GetSupportedThinkingLevels(m.ToModel())
		want := []ModelThinkingLevel{ThinkingMinimal, ThinkingLow, ThinkingMedium, ThinkingHigh}
		if len(levels) != len(want) {
			t.Fatalf("supported thinking levels = %v, want %v", levels, want)
		}
		for i := range want {
			if levels[i] != want[i] {
				t.Fatalf("supported thinking levels = %v, want %v", levels, want)
			}
		}
	})
}

func TestGeneratedCatalog0861TranscriptMetadata(t *testing.T) {
	generated, ok := LookupModelExact("anthropic/claude-fable-5")
	if !ok {
		t.Fatal("anthropic/claude-fable-5 missing")
	}
	if generated.PromptCache["short"] != 300 || generated.PromptCache["long"] != 3600 {
		t.Fatalf("prompt cache = %v", generated.PromptCache)
	}
	compat := generated.Compat
	if compat == nil || compat.SupportsMidConvoSystemMessages == nil || !*compat.SupportsMidConvoSystemMessages || compat.SupportsMidConvoToolChanges == nil || !*compat.SupportsMidConvoToolChanges {
		t.Fatalf("mid-conversation compat = %+v", compat)
	}
	if len(compat.AllowedFallbackModels) != 2 || compat.AllowedFallbackModels[0].Model != "claude-opus-4-8" {
		t.Fatalf("allowed fallback models = %+v", compat.AllowedFallbackModels)
	}

	model := generated.ToModel()
	model.PromptCache["short"] = 1
	model.ProviderMeta.Compat.AllowedFallbackModels[0].Model = "changed"
	if generated.PromptCache["short"] != 300 || generated.Compat.AllowedFallbackModels[0].Model != "claude-opus-4-8" {
		t.Fatal("ToModel returned aliases into generated catalog metadata")
	}
}

func TestListModelsFilter(t *testing.T) {
	all := ListModels("")
	openai := ListModels("openai")
	if len(openai) == 0 {
		t.Fatal("no openai models")
	}
	if len(openai) >= len(all) {
		t.Fatalf("openai-only subset (%d) should be smaller than all (%d)", len(openai), len(all))
	}
	for _, m := range openai {
		if m.Provider != "openai" {
			t.Fatalf("filter leaked %q", m.Provider)
		}
	}
}

// Pi 2e0f175b: the catalog is generated from models.dev's v2 catalog, whose realtime and image models are not chat models, so
// `gpt-realtime-2.1`, `gemini-3.1-flash-live-preview` and `gemini-3.1-flash-lite-image` are no longer offered (packages/ai/CHANGELOG.md, Unreleased).
func TestGeneratedCatalogOmitsRealtimeAndImageModelsAsChatModels(t *testing.T) {
	for _, spec := range []string{"azure/gpt-realtime-2.1", "openai/gpt-realtime-2.1", "google/gemini-3.1-flash-live-preview", "google/gemini-3.1-flash-lite-image"} {
		if m, ok := LookupModel(spec); ok {
			t.Errorf("LookupModel(%q) = %s/%s, want no chat model", spec, m.Provider, m.ID)
		}
	}
}

// TestCodegenByteIdentical re-runs the catalog generator against temp paths and
// checks the chat, image and classifier outputs match the committed files
// byte-for-byte. It reads the same source as `make model-catalogs`: the
// published pi-ai barrel when installed, otherwise the pinned upstream mirror.
func TestCodegenByteIdentical(t *testing.T) {
	repoRoot, err := findRepoRoot()
	if err != nil {
		t.Skip("can't find repo root:", err)
	}
	src := resolveModelSource(repoRoot)
	if src == "" {
		pioracle.Missing(t, "a model source (the published pi-ai or the upstream mirror)", nil)
	}
	dir := t.TempDir()
	outputs := map[string]string{
		"ai/models_generated.go":            filepath.Join(dir, "models_generated.go"),
		"ai/image_models_generated.go":      filepath.Join(dir, "image_models_generated.go"),
		"ai/classifier_models_generated.go": filepath.Join(dir, "classifier_models_generated.go"),
		"ai/providers_generated.go":         filepath.Join(dir, "providers_generated.go"),
	}
	cmd := exec.Command("go", "run", "./cmd/gen-models", "-src", src, "-out", outputs["ai/models_generated.go"], "-image-out", outputs["ai/image_models_generated.go"], "-classifier-out", outputs["ai/classifier_models_generated.go"], "-provider-out", outputs["ai/providers_generated.go"])
	cmd.Dir = repoRoot
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("gen-models failed: %v\n%s", err, out)
	}
	for committedPath, regeneratedPath := range outputs {
		committed, err := os.ReadFile(filepath.Join(repoRoot, committedPath))
		if err != nil {
			t.Fatal(err)
		}
		regenerated, err := os.ReadFile(regeneratedPath)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(normalizeSourceComment(committed), normalizeSourceComment(regenerated)) {
			t.Errorf("%s drift\ncommitted=%d bytes regenerated=%d bytes: re-run `go generate ./ai/...`", committedPath, len(committed), len(regenerated))
		}
	}
	// The record chunks are compared inflated: a gzip stream's bytes depend on the compressor's version, its content does not.
	regeneratedChunks, err := os.ReadDir(filepath.Join(dir, "catalog_records"))
	if err != nil {
		t.Fatal(err)
	}
	committedChunks, err := catalogRecordFiles.ReadDir("catalog_records")
	if err != nil {
		t.Fatal(err)
	}
	if len(regeneratedChunks) != len(committedChunks) {
		t.Fatalf("catalog_records drift: %d chunks committed, %d regenerated", len(committedChunks), len(regeneratedChunks))
	}
	for _, entry := range regeneratedChunks {
		regenerated, err := os.ReadFile(filepath.Join(dir, "catalog_records", entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		committed, err := catalogRecordFiles.ReadFile("catalog_records/" + entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		want, err1 := inflateCatalogChunk(regenerated)
		got, err2 := inflateCatalogChunk(committed)
		if err1 != nil || err2 != nil || !bytes.Equal(got, want) {
			t.Errorf("catalog_records/%s drift: re-run `go generate ./ai/...`", entry.Name())
		}
	}
}

// sourceCommentRE matches the generated header's `// Source: models.generated.<ext>`
// line. cmd/gen-models embeds basename(src), which keeps the file extension:
// `go generate` (doc.go) feeds the published package's compiled models.generated.js,
// while a mirror-only checkout feeds the mirror's models.generated.ts. The extension
// is metadata, not model data; the catalogs must still match byte-for-byte.
// Normalizing only this line keeps the comparison a true drift check on model data
// while tolerating the two equivalent upstream sources' differing extensions.
var sourceCommentRE = regexp.MustCompile(`(?m)^// Source: models\.generated\.(?:ts|js)$`)

// normalizeSourceComment leaves the `// Upstream: @earendil-works/pi-ai <version>` line in place. Both sources emit it from
// the pi-ai manifest one directory above the source (dist/../package.json or the mirror's src/../package.json), so the
// comparison also proves the committed header names the release the catalog was generated from.
func normalizeSourceComment(b []byte) []byte {
	return sourceCommentRE.ReplaceAll(b, []byte("// Source: models.generated"))
}

func findRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", os.ErrNotExist
		}
		dir = parent
	}
}

// resolveModelSource returns the path to the parity-authoritative model
// file. Prefers the installed pi binary's bundled model file (what
// `make parity` compares against) over the git-tag mirror, since npm
// publishes can include model spec updates after the tag is cut.
func resolveModelSource(repoRoot string) string {
	published := filepath.Join(repoRoot, "extensions", "sdk-ts", "node_modules", "@earendil-works", "pi-coding-agent", "node_modules", "@earendil-works", "pi-ai", "dist", "models.generated.js")
	if _, err := os.Stat(published); err == nil {
		return published
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return resolveGitTagSource(repoRoot)
	}
	ver := upstreamPackageVersion()
	for _, pkg := range []string{
		"npm-earendil-works-pi-coding-agent",
		"npm-mariozechner-pi-coding-agent",
	} {
		for _, sub := range []string{
			"@earendil-works/pi-coding-agent/node_modules/@earendil-works/pi-ai",
			"@mariozechner/pi-coding-agent/node_modules/@mariozechner/pi-ai",
		} {
			p := filepath.Join(home, ".local", "share", "mise", "installs",
				pkg, ver, "lib", "node_modules", filepath.FromSlash(sub),
				"dist", "models.generated.js")
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
	}
	return resolveGitTagSource(repoRoot)
}

func resolveGitTagSource(repoRoot string) string {
	p := filepath.Join(repoRoot, ".upstream", "v"+UpstreamVersionString(), "packages", "ai", "src", "models.generated.ts")
	if _, err := os.Stat(p); err == nil {
		return p
	}
	return ""
}

// UpstreamVersionString returns the shared upstream version without importing
// coding, which would create a cycle.
func UpstreamVersionString() string {
	return pigversion.UpstreamVersion
}

// upstreamPackageVersion is the version the pinned Pi build's package.json files report: UpstreamVersionString for a release
// tag, without the +<commit> of a commit pin.
func upstreamPackageVersion() string {
	return pigversion.UpstreamPackageVersion
}

// providers/all.ts:99 getBuiltinModelDataGeneratedAt: the generation time of the built-in catalogs, absent when the manifest carries none. The caller gets a copy, so changing it does not change the catalog's time.
func TestGetBuiltinModelDataGeneratedAtReturnsACopyOfTheManifestTime(t *testing.T) {
	original := GeneratedModelDataGeneratedAt
	t.Cleanup(func() { GeneratedModelDataGeneratedAt = original })

	GeneratedModelDataGeneratedAt = new(float64(1234567890123))
	got := GetBuiltinModelDataGeneratedAt()
	if got == nil || *got != 1234567890123 {
		t.Fatalf("GetBuiltinModelDataGeneratedAt = %v, want 1234567890123", got)
	}
	*got = 1
	if again := GetBuiltinModelDataGeneratedAt(); again == nil || *again != 1234567890123 {
		t.Fatalf("a caller's change reached the catalog time: %v", again)
	}

	GeneratedModelDataGeneratedAt = nil
	if got := GetBuiltinModelDataGeneratedAt(); got != nil {
		t.Fatalf("without a manifest time = %v, want nil", got)
	}
}
