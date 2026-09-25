package subprocess

import (
	"context"
	"errors"
	"fmt"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/runtimecell"
)

// LoadRustPackedCell starts a generated Rust packed-cell runner and registers
// every contained extension atomically. It mirrors LoadGoPackedCell. Each
// extension keeps one socket and one register handshake on the current wire.
func (h *Host) LoadRustPackedCell(ctx context.Context, cell *runtimecell.RustPackedCell) ([]extension.Extension, error) {
	staged, registered, err := h.startRustPackedCell(ctx, cell)
	if err != nil {
		if _, ok := errors.AsType[*packedAcceptError](err); ok {
			h.rollbackPartialPackedCell(staged)
		}
		return nil, err
	}
	h.commitStaged(staged, nil, "packed replaced")
	return registered, nil
}

func (h *Host) startRustPackedCell(ctx context.Context, cell *runtimecell.RustPackedCell) ([]stagedManagedExt, []extension.Extension, error) {
	startCell, err := rustStartCell(cell)
	if err != nil {
		return nil, nil, err
	}
	return h.startGoPackedCell(ctx, startCell)
}

func rustStartCell(cell *runtimecell.RustPackedCell) (*runtimecell.GoPackedCell, error) {
	if cell == nil {
		return nil, newLoadError("packed-rust-cell", "resolve", "missing_cell", fmt.Errorf("nil packed Rust cell"))
	}
	start := &runtimecell.GoPackedCell{
		Key:        cell.Key,
		Hash:       cell.Hash,
		CacheDir:   cell.CacheDir,
		BinaryPath: cell.BinaryPath,
		Cached:     cell.Cached,
		Extensions: make([]runtimecell.GoExtension, 0, len(cell.Extensions)),
	}
	for _, ext := range cell.Extensions {
		start.Extensions = append(start.Extensions, runtimecell.GoExtension{Name: ext.Name, Root: ext.Root})
	}
	return start, nil
}
