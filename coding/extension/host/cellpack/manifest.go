// Package cellpack is the Piglet-Binary-side counterpart to the host's prebuilt-cell
// seam (runtimecell.PrebuiltResolver). A Piglet Binary embeds its packed cells plus a
// manifest describing each cell's composition; at startup it extracts the
// binaries and registers a Resolver so the host serves them without any Go/Rust
// toolchain.
//
// Matching is by composition (language + cell key + target + extension set),
// which is stable across machines, rather than the host's from-source build
// hash, which embeds the toolchain version and is not reproducible on a
// toolchain-less consumer.
package cellpack

import (
	"os"
	"sort"
	"strings"

	"github.com/MichaelKinsy/PiG/coding/extension/host/runtimecell"
)

// Manifest describes the cells embedded in a Piglet Binary.
type Manifest struct {
	PigCoreVersion string      `json:"pigCoreVersion"`
	Cells          []CellEntry `json:"cells"`
}

// CellEntry is one prebuilt cell: its composition and where its binary lives
// relative to the extraction directory.
type CellEntry struct {
	Language   string     `json:"language"` // go | rust | python
	Key        string     `json:"key"`
	Strategy   string     `json:"strategy,omitempty"` // isolated | packed-go | packed-rust | packed-python
	OS         string     `json:"os"`
	Arch       string     `json:"arch"`
	Binary     string     `json:"binary"` // path relative to the extraction dir
	Extensions []ExtEntry `json:"extensions"`
}

// ExtEntry pins one extension's identity in a cell.
type ExtEntry struct {
	Name string `json:"name"`
	Hash string `json:"hash,omitempty"`
}

// Resolver serves embedded cells by composition. It implements
// runtimecell.PrebuiltResolver.
type Resolver struct {
	baseDir string
	index   map[string]CellEntry
}

// NewResolver indexes a manifest against the directory its binaries were
// extracted into.
func NewResolver(m Manifest, baseDir string) *Resolver {
	index := make(map[string]CellEntry, len(m.Cells))
	for _, c := range m.Cells {
		index[compositionKey(c.Language, c.Key, c.OS, c.Arch, entryNames(c.Extensions))] = c
	}
	return &Resolver{baseDir: baseDir, index: index}
}

// ResolvePrebuilt returns the embedded binary for a request's composition, or
// ok=false to let the host build from source. It fails closed: a pinned hash
// that disagrees with the request, or a missing extracted binary, is a miss.
func (r *Resolver) ResolvePrebuilt(req runtimecell.PrebuiltRequest) (string, bool) {
	names := make([]string, len(req.Extensions))
	reqHash := make(map[string]string, len(req.Extensions))
	for i, e := range req.Extensions {
		names[i] = e.Name
		reqHash[e.Name] = e.Hash
	}

	entry, ok := r.index[compositionKey(req.Language, req.Key, req.GOOS, req.GOARCH, names)]
	if !ok {
		return "", false
	}
	if !hashesAgree(entry.Extensions, reqHash) {
		return "", false
	}
	bin := extractedBinaryPath(r.baseDir, entry.Binary)
	if _, err := os.Stat(bin); err != nil {
		return "", false
	}
	return bin, true
}

// hashesAgree enforces pinned hashes only when both sides know them. A missing
// or "unknown" hash on either side matches on names alone, so both
// source-shipping and source-less Piglet Binaries work; two known, differing hashes
// are a mismatch.
func hashesAgree(pinned []ExtEntry, reqHash map[string]string) bool {
	for _, p := range pinned {
		if !known(p.Hash) {
			continue
		}
		rh, ok := reqHash[p.Name]
		if !ok || !known(rh) {
			continue
		}
		if p.Hash != rh {
			return false
		}
	}
	return true
}

func known(h string) bool { return h != "" && h != "unknown" }

func entryNames(exts []ExtEntry) []string {
	names := make([]string, len(exts))
	for i, e := range exts {
		names[i] = e.Name
	}
	return names
}

func compositionKey(lang, key, goos, goarch string, names []string) string {
	sorted := append([]string(nil), names...)
	sort.Strings(sorted)
	return strings.Join(append([]string{lang, key, goos, goarch}, sorted...), "\x00")
}
