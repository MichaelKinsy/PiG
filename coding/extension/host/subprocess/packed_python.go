package subprocess

import (
	"context"
	"errors"
	"fmt"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/runtimecell"
)

// LoadPythonPackedCell starts a generated Python packed-cell runner and
// registers every contained extension atomically. It mirrors the Go/Rust paths
// and keeps the current subprocess wire with one socket/register handshake per extension.
func (h *Host) LoadPythonPackedCell(ctx context.Context, cell *runtimecell.PythonPackedCell) ([]extension.Extension, error) {
	staged, registered, err := h.startPythonPackedCell(ctx, cell)
	if err != nil {
		if _, ok := errors.AsType[*packedAcceptError](err); ok {
			h.rollbackPartialPackedCell(staged)
		}
		return nil, err
	}
	h.commitStaged(staged, nil, "packed replaced")
	return registered, nil
}

func (h *Host) startPythonPackedCell(ctx context.Context, cell *runtimecell.PythonPackedCell) ([]stagedManagedExt, []extension.Extension, error) {
	startCell, err := pythonStartCell(cell)
	if err != nil {
		return nil, nil, err
	}
	return h.startGoPackedCell(ctx, startCell)
}

func pythonStartCell(cell *runtimecell.PythonPackedCell) (*runtimecell.GoPackedCell, error) {
	if cell == nil {
		return nil, newLoadError("packed-python-cell", "resolve", "missing_cell", fmt.Errorf("nil packed Python cell"))
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
