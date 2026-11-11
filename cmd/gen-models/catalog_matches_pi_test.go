package main

// pi: packages/ai/catalog/define.ts
// pi: packages/ai/catalog/generate.ts
// pi: packages/ai/catalog/families/anthropic.ts
// pi: packages/ai/catalog/families/openai.ts
// pi: packages/ai/catalog/families/deepseek.ts
// pi: packages/ai/catalog/families/google.ts
// pi: packages/ai/catalog/families/kimi.ts
// pi: packages/ai/catalog/providers/amazon-bedrock.ts
// pi: packages/ai/catalog/providers/ant-ling.ts
// pi: packages/ai/catalog/providers/anthropic.ts
// pi: packages/ai/catalog/providers/azure.ts
// pi: packages/ai/catalog/providers/baseten.ts
// pi: packages/ai/catalog/providers/cerebras.ts
// pi: packages/ai/catalog/providers/cloudflare-ai-gateway.ts
// pi: packages/ai/catalog/providers/cloudflare-workers-ai.ts
// pi: packages/ai/catalog/providers/deepseek.ts
// pi: packages/ai/catalog/providers/fireworks.ts
// pi: packages/ai/catalog/providers/github-copilot.ts
// pi: packages/ai/catalog/providers/google-vertex.ts
// pi: packages/ai/catalog/providers/google.ts
// pi: packages/ai/catalog/providers/groq.ts
// pi: packages/ai/catalog/providers/huggingface.ts
// pi: packages/ai/catalog/providers/index.ts
// pi: packages/ai/catalog/providers/kimi-coding.ts
// pi: packages/ai/catalog/providers/meta.ts
// pi: packages/ai/catalog/providers/minimax-cn.ts
// pi: packages/ai/catalog/providers/minimax.ts
// pi: packages/ai/catalog/providers/mistral.ts
// pi: packages/ai/catalog/providers/moonshotai-cn.ts
// pi: packages/ai/catalog/providers/moonshotai.ts
// pi: packages/ai/catalog/providers/nvidia.ts
// pi: packages/ai/catalog/providers/openai-codex.ts
// pi: packages/ai/catalog/providers/openai.ts
// pi: packages/ai/catalog/providers/opencode-go.ts
// pi: packages/ai/catalog/providers/opencode.ts
// pi: packages/ai/catalog/providers/openrouter.ts
// pi: packages/ai/catalog/providers/qwen-token-plan-cn.ts
// pi: packages/ai/catalog/providers/qwen-token-plan-individual.ts
// pi: packages/ai/catalog/providers/qwen-token-plan.ts
// pi: packages/ai/catalog/providers/radius.ts
// pi: packages/ai/catalog/providers/together.ts
// pi: packages/ai/catalog/providers/typesafe.ts
// pi: packages/ai/catalog/providers/vercel-ai-gateway.ts
// pi: packages/ai/catalog/providers/xai.ts
// pi: packages/ai/catalog/providers/xiaomi-token-plan-ams.ts
// pi: packages/ai/catalog/providers/xiaomi-token-plan-cn.ts
// pi: packages/ai/catalog/providers/xiaomi-token-plan-sgp.ts
// pi: packages/ai/catalog/providers/xiaomi.ts
// pi: packages/ai/catalog/providers/zai-coding-cn.ts
// pi: packages/ai/catalog/providers/zai.ts

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The fixtures are upstream responses (models.dev v2, OpenRouter, Vercel AI Gateway, NVIDIA NIM, Radius) pruned by
// test/parity/testdata/catalog-generation/prune.mjs from a live recording; golden.sha256 lists the sha256 of every file Pi's own
// generator (packages/ai/catalog/generate.ts at the pin) writes for them (equivalence.sh golden). The Go generator must write the
// same bytes: this is the regenerated-catalog evidence of the catalog definitions (packages/ai/catalog/**), and of the
// header-comment-only provider shards and models.generated.ts that depend on them.
func TestCatalogGenerationMatchesPi(t *testing.T) {
	fixtures := t.TempDir()
	unpackFixtures(t, filepath.Join("testdata", "catalog-generation", "fixtures.tar.gz"), fixtures)
	if err := replayResponses(fixtures); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { http.DefaultClient.Transport = nil })

	output := t.TempDir()
	options := generatorOptions{strict: true, dataOutputDir: filepath.Join(output, "data"), jsonOutputDir: filepath.Join(output, "catalog")}
	if err := generateModels(options); err != nil {
		t.Fatal(err)
	}

	want := readGolden(t, filepath.Join("testdata", "catalog-generation", "golden.sha256"))
	got := map[string]string{}
	err := filepath.WalkDir(output, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || entry.Name() == ".manifest.json" {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(output, path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		got[filepath.ToSlash(relative)] = hex.EncodeToString(sum[:])
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for name, digest := range want {
		switch have, ok := got[name]; {
		case !ok:
			t.Errorf("%s: not generated (Pi writes it)", name)
		case have != digest:
			t.Errorf("%s: sha256 %s, Pi's generator wrote %s", name, have, digest)
		}
	}
	for name := range got {
		if _, ok := want[name]; !ok {
			t.Errorf("%s: generated, but Pi's generator does not write it", name)
		}
	}
	if t.Failed() {
		t.Log("compare with Pi's generator: test/parity/testdata/catalog-generation/equivalence.sh compare <fixtures>")
	}
}

func readGolden(t *testing.T, path string) map[string]string {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	golden := map[string]string{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		digest, name, ok := strings.Cut(scanner.Text(), "  ")
		if !ok {
			t.Fatalf("%s: malformed line %q", path, scanner.Text())
		}
		golden[filepath.ToSlash(strings.TrimPrefix(name, "./"))] = digest
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return golden
}

func unpackFixtures(t *testing.T, archive, dir string) {
	t.Helper()
	file, err := os.Open(archive)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	zipped, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	reader := tar.NewReader(zipped)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		if header.Typeflag != tar.TypeReg {
			continue
		}
		data, err := io.ReadAll(reader)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, filepath.Base(header.Name)), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
