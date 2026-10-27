package subprocess

import (
	"context"
	"errors"
	"fmt"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/runtimecell"
)

// LoadLispPackedCell starts a generated Lisp runner image and registers every contained extension
// atomically. It mirrors the Python path and keeps the current subprocess wire with one socket and
// one register handshake per extension.
func (h *Host) LoadLispPackedCell(ctx context.Context, cell *runtimecell.LispPackedCell) ([]extension.Extension, error) {
	staged, registered, err := h.startLispPackedCell(ctx, cell)
	if err != nil {
		if _, ok := errors.AsType[*packedAcceptError](err); ok {
			h.rollbackPartialPackedCell(staged)
		}
		return nil, err
	}
	h.commitStaged(staged, nil, "packed replaced")
	return registered, nil
}

func (h *Host) startLispPackedCell(ctx context.Context, cell *runtimecell.LispPackedCell) ([]stagedManagedExt, []extension.Extension, error) {
	startCell, err := lispStartCell(cell)
	if err != nil {
		return nil, nil, err
	}
	return h.startGoPackedCell(ctx, startCell)
}

func lispStartCell(cell *runtimecell.LispPackedCell) (*runtimecell.GoPackedCell, error) {
	if cell == nil {
		return nil, newLoadError("packed-lisp-cell", "resolve", "missing_cell", fmt.Errorf("nil packed Lisp cell"))
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
