package subprocess

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/runtimecell"
)

// nodeExtension is one TS/JS extension resolved for a packed Node cell.
type nodeExtension struct {
	Name  string
	Entry string
	Hash  string
}

// NodeExtensions resolves each member's entrypoint the same way an isolated
// Node extension does (resolveNodeEntrypoint), preserving the cell's plan
// order.
func (c CellSpec) NodeExtensions() ([]nodeExtension, error) {
	if c.Strategy != CellStrategyPackedNode {
		return nil, fmt.Errorf("cell %s is %s, not packed-node", c.Key, c.Strategy)
	}
	out := make([]nodeExtension, 0, len(c.Extensions))
	for _, cfg := range c.Extensions {
		if cfg.Source == "" {
			return nil, fmt.Errorf("extension %q missing source for packed-node cell", cfg.Name)
		}
		entry, err := resolveNodeEntrypoint(cfg.Source)
		if err != nil {
			return nil, fmt.Errorf("extension %q: %w", cfg.Name, err)
		}
		out = append(out, nodeExtension{Name: cfg.Name, Entry: entry, Hash: valueOr(cfg.ContentHash, entry)})
	}
	return out, nil
}

// nodePackedCell is the artifact for a Node cell: one copy of the embedded
// Node runtime plus a manifest naming every member's socket-env name and
// resolved entry, loaded in plan order by runtime-node/cell.mjs. Unlike a
// Go/Rust/Python packed cell nothing is compiled here: TS/JS source is read
// fresh at process start by the same type-stripping loader an isolated Node
// extension uses (builder_node.go), so the cache holds only the runtime copy
// and the manifest, not the extension source.
type nodePackedCell struct {
	Key        string
	Hash       string
	BinaryPath string
	Cached     bool
	Extensions []nodeExtension
}

type nodeCellManifestEntry struct {
	Name    string `json:"name"`
	Entry   string `json:"entry"`
	SockEnv string `json:"sockEnv"`
}

// nodePackedLauncherManifest recognizes the published Node cell's runner and
// reads the member order used for its process arguments.
func nodePackedLauncherManifest(binPath string) ([]nodeCellManifestEntry, bool) {
	if filepath.Base(binPath) != "runner" {
		return nil, false
	}
	data, err := os.ReadFile(filepath.Join(filepath.Dir(binPath), "manifest.json"))
	if err != nil {
		return nil, false
	}
	var manifest []nodeCellManifestEntry
	if err := json.Unmarshal(data, &manifest); err != nil || len(manifest) == 0 {
		return nil, false
	}
	return manifest, true
}

func nodePackedLauncherCommand(ctx context.Context, binPath string) (*exec.Cmd, bool) {
	manifest, ok := nodePackedLauncherManifest(binPath)
	if !ok {
		return nil, false
	}
	dir := filepath.Dir(binPath)
	loaderPath := filepath.Join(dir, "runtime", "register-loader.mjs")
	loaderURL, urlErr := nodeFileURL(loaderPath)
	args := []string{"--import", loaderURL, filepath.Join(dir, "runtime", "cell.mjs"), filepath.Join(dir, "manifest.json")}
	// Keep member source paths visible to process inspection without shell quoting.
	for _, member := range manifest {
		args = append(args, member.Entry)
	}
	cmd := exec.CommandContext(ctx, "node", args...)
	if urlErr != nil && cmd.Err == nil {
		cmd.Err = fmt.Errorf("node cell loader %s: %w", loaderPath, urlErr)
	}
	return cmd, true
}

// nodePackedCellHash derives the deterministic cache key for a Node cell from
// its plan order, so buildNodePackedCell and CurrentNodePackedCellEntry agree
// without either running the other. Adding, removing, or reordering a member
// always changes it.
func nodePackedCellHash(cellKey string, exts []nodeExtension) (string, []nodeCellManifestEntry) {
	digest := sha256.New()
	_, _ = digest.Write([]byte(nodeLauncherFormat + "\x00" + cellKey + "\x00"))
	manifest := make([]nodeCellManifestEntry, 0, len(exts))
	for _, ext := range exts {
		_, _ = digest.Write([]byte(ext.Name + "\x00" + ext.Entry + "\x00"))
		manifest = append(manifest, nodeCellManifestEntry{Name: ext.Name, Entry: ext.Entry, SockEnv: runtimecell.SocketEnvName(ext.Name)})
	}
	return hex.EncodeToString(digest.Sum(nil)), manifest
}

func nodePackedCellDir(cacheRoot, hash string) string {
	return filepath.Join(cacheRoot, "cells", "node", hash)
}

// CurrentNodePackedCellEntry reports the cache entry the given members would
// resolve to, without building anything. It mirrors
// runtimecell.CurrentGoPackedCellEntry for the Node cell's GC accounting.
func CurrentNodePackedCellEntry(cacheRoot, cellKey string, exts []nodeExtension) (string, bool, error) {
	if len(exts) == 0 {
		return "", false, fmt.Errorf("packed node cell %s has no extensions", cellKey)
	}
	hash, _ := nodePackedCellHash(cellKey, exts)
	cellDir := nodePackedCellDir(cacheRoot, hash)
	entry, ok := runtimecell.ReusePublishedArtifact(cellDir, runtimecell.EntryIdentity{InputDigest: hash, Artifact: "runner", Language: "node"})
	if !ok {
		return cellDir, false, nil
	}
	return entry.Dir, true, nil
}

// buildNodePackedCell prepares (or reuses) the cache directory hosting a Node
// cell. Unlike a single isolated Node launcher's build (builder.go, also
// backed by runtimecell.PublishArtifact), several extensions can race to
// build the *same* cell key from independent Pig instances sharing one cache
// root; PublishArtifact is the same content-addressed, per-digest-locked,
// atomic-rename publication every packed Go/Rust/Python cell and every
// source-mode extension binary already uses, so a concurrent cold build here
// gets the same exactly-one-builder guarantee instead of two builders racing
// on fixed "manifest.json.tmp"/"runner.tmp" names.
func buildNodePackedCell(ctx context.Context, cacheRoot, cellKey string, exts []nodeExtension) (*nodePackedCell, error) {
	if len(exts) == 0 {
		return nil, fmt.Errorf("packed node cell %s has no extensions", cellKey)
	}
	hash, manifest := nodePackedCellHash(cellKey, exts)
	cellDir := nodePackedCellDir(cacheRoot, hash)

	entry, err := runtimecell.PublishArtifact(ctx, cellDir, "runner", hash, "node", func(scratch string) (string, error) {
		runtimeDir := filepath.Join(scratch, "runtime")
		if err := os.MkdirAll(filepath.Join(runtimeDir, "shims"), 0o755); err != nil {
			return "", err
		}
		if err := copyEmbeddedTree(nodeRuntimeFS, "runtime-node", runtimeDir); err != nil {
			return "", err
		}
		data, err := json.Marshal(manifest)
		if err != nil {
			return "", err
		}
		manifestPath := filepath.Join(scratch, "manifest.json")
		if err := os.WriteFile(manifestPath, data, 0o644); err != nil {
			return "", err
		}
		launcherPath := filepath.Join(scratch, "runner")
		launcher := "pig-node-launcher:" + nodeLauncherFormat + "\n"
		if err := os.WriteFile(launcherPath, []byte(launcher), 0o644); err != nil {
			return "", err
		}
		return launcherPath, nil
	}, "runtime", "manifest.json")
	if err != nil {
		return nil, err
	}
	return &nodePackedCell{Key: cellKey, Hash: hash, BinaryPath: entry.ArtifactPath, Cached: entry.Reused, Extensions: exts}, nil
}

// LoadNodePackedCell starts a generated Node packed-cell runner and registers
// every contained extension atomically. It mirrors LoadGoPackedCell: each
// extension keeps its own socket and its own register handshake.
func (h *Host) LoadNodePackedCell(ctx context.Context, cell *nodePackedCell) ([]extension.Extension, error) {
	staged, registered, err := h.startNodePackedCell(ctx, cell, h.nextPackedCellGeneration(cell.Key))
	if err != nil {
		if _, ok := errors.AsType[*packedAcceptError](err); ok {
			h.rollbackPartialPackedCell(staged)
		}
		return nil, err
	}
	h.commitStaged(staged, nil, "packed replaced")
	return registered, nil
}

// startNodePackedCell spawns cell. generation is this attempt's already-claimed
// packedCellGeneration value (CNC-002): callers claim it themselves, as early
// as possible (before any build/cache work, which can briefly block on a
// lock), so an async crash report for a process this attempt is replacing has
// the smallest possible window to still see itself as "current" and wrongly
// tear this attempt down.
func (h *Host) startNodePackedCell(ctx context.Context, cell *nodePackedCell, generation int) ([]stagedManagedExt, []extension.Extension, error) {
	if cell == nil {
		return nil, nil, newLoadError("packed-node-cell", "resolve", "missing_cell", fmt.Errorf("nil packed Node cell"))
	}
	start := &runtimecell.GoPackedCell{
		Key:        cell.Key,
		Hash:       cell.Hash,
		BinaryPath: cell.BinaryPath,
		Cached:     cell.Cached,
		Extensions: make([]runtimecell.GoExtension, 0, len(cell.Extensions)),
		Generation: generation,
	}
	for _, ext := range cell.Extensions {
		start.Extensions = append(start.Extensions, runtimecell.GoExtension{Name: ext.Name, Root: ext.Entry, Hash: ext.Hash})
	}
	return h.startGoPackedCell(ctx, start)
}
