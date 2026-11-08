package tui

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestMarkdownDifferentialDump writes a digest of what the block lexer and the renderer make of a large corpus to the file named by PIG_MARKDOWN_DIFF_OUT, one line per document and width, and does nothing otherwise. It is the tool for changing the lexer or the renderer without changing its output: run it on the commit before the change and on the one after, and compare the two files (cmp); a line that differs names the document, and PIG_MARKDOWN_DIFF_ONLY=<name> prints that document's token dump and rendered lines.
//
// The corpus is every document of the committed Pi-oracle corpora (markedLexerCommittedSources), the markdown files of the repository, a long answer and a streaming body at each prefix a streamed message passes through (rendered from scratch and by one component that is fed the prefixes in turn), every seventh source of the block-tokenizer sources, and generated documents of every shape the lexer oracle draws.
func TestMarkdownDifferentialDump(t *testing.T) {
	path := os.Getenv("PIG_MARKDOWN_DIFF_OUT")
	if path == "" {
		t.Skip("PIG_MARKDOWN_DIFF_OUT is not set")
	}
	mdCaseSetup(t)
	only := os.Getenv("PIG_MARKDOWN_DIFF_ONLY")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	out := bufio.NewWriter(file)
	t.Cleanup(func() {
		if err := out.Flush(); err != nil {
			t.Error(err)
		}
		if err := file.Close(); err != nil {
			t.Error(err)
		}
	})
	emit := func(format string, args ...any) {
		if _, err := fmt.Fprintf(out, format, args...); err != nil {
			t.Fatal(err)
		}
	}

	widths := []int{2, 4, 17, 60, 100}
	state := &markedInlineState{}
	digest := func(name, src string, render bool) {
		tokens := lexMarkedBlocks(state, src)
		state.startDocument()
		sum := sha256.Sum256([]byte(tokens))
		line := fmt.Sprintf("%s tokens %s", name, hex.EncodeToString(sum[:8]))
		if only == name {
			emit("%s\n%s\n", line, tokens)
		} else {
			emit("%s\n", line)
		}
		if !render {
			return
		}
		for _, width := range widths {
			lines := NewMarkdownWithOptions(src, 0, 0, mdUpstreamTheme(), nil, nil).Render(width)
			sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
			emit("%s render %d %s\n", name, width, hex.EncodeToString(sum[:8]))
			if only == name {
				emit("%q\n", lines)
			}
		}
	}

	sets := markedLexerCommittedSources(t)
	names := make([]string, 0, len(sets))
	for name := range sets {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		for i, src := range sets[name] {
			digest(fmt.Sprintf("%s/%d", name, i), src, true)
		}
	}

	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	var files []string
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() && (d.Name() == ".git" || d.Name() == "node_modules" || d.Name() == ".upstream") {
			return filepath.SkipDir
		}
		if !d.IsDir() && strings.HasSuffix(p, ".md") {
			files = append(files, p)
		}
		return nil
	})
	slices.Sort(files)
	for _, p := range files {
		data, err := os.ReadFile(p)
		if err != nil || len(data) > 400<<10 {
			continue
		}
		rel, _ := filepath.Rel(root, p)
		digest("file/"+rel, string(data), true)
	}

	for _, doc := range []struct {
		name, body string
	}{{"long-answer", longAnswer(20000)}, {"streaming-body", streamingBody(8000)}} {
		streamed := NewMarkdownWithOptions("", 0, 0, mdUpstreamTheme(), nil, nil)
		for end := 24; ; end += 24 {
			end = min(end, len(doc.body))
			digest(fmt.Sprintf("%s/%d", doc.name, end), doc.body[:end], end%120 == 0 || end == len(doc.body))
			streamed.SetText(doc.body[:end])
			for _, width := range []int{60, 100} {
				sum := sha256.Sum256([]byte(strings.Join(streamed.Render(width), "\n")))
				emit("%s/%d streamed %d %s\n", doc.name, end, width, hex.EncodeToString(sum[:8]))
			}
			if end == len(doc.body) {
				break
			}
		}
	}

	i := 0
	markedBlockSources(func(src string) {
		if i++; i%7 == 0 {
			digest(fmt.Sprintf("block-source/%d", i), src, false)
		}
	})

	for seed := range uint64(2) {
		rng := rand.New(rand.NewPCG(seed+1, 0xb5ad4eceda1ce2a9))
		for n := range 60000 {
			digest(fmt.Sprintf("generated/%d/%d", seed, n), markedLexerDocument(rng), n%5 == 0)
		}
	}
}
