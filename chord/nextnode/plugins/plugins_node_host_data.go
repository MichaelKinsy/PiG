package plugins

import (
	"context"

	"github.com/MichaelKinsy/PiG/chord/next/contracts"
)

// No Pi file: the control requests with which the app reaches the host data operations of the subprocess's host (D462).
//
// Pi's server has one host: the app calls its `exportHostData`, `importHostData` and `deleteHostData`, which reach a plugin's keyed
// documents and `host(...)` rows. PiG's plugins run in this subprocess, whose host has its own store, so the app asks the process
// to run Pi's three operations on its host. The archives are JSONL files at paths the app names (both live on the same machine):
// the process writes or reads them as Pi's `app.ts` does (`encodeJsonl`, `decodeJsonl`).
//
// pig divergence (D462): the server's data is two parts, this host's and the Go host's; Pi's `app.ts` calls one host.

// hostDataRequest is the `exportHostData`, `importHostData` and `deleteHostData` control requests of host.mjs.
type hostDataRequest struct {
	envelope
	Address addressWire `json:"address"`
	// Path is the JSONL archive file to write (export) or read (import); Existing is the import's `existing` ("" omitted: Pi's default).
	Path     string `json:"path,omitempty"`
	Existing string `json:"existing,omitempty"`
}

func wireOf(address contracts.Address) addressWire {
	return addressWire{Kind: address.Kind, Params: address.Params, Key: address.Key}
}

// ExportHostData writes the records and rows of the process's host belonging to address as a JSONL archive at path (mode 0600): Pi's
// `host.exportHostData` encoded with `encodeJsonl`. It returns once the file is written; an abort of ctx aborts the export, which
// gives its commit queue back. It must not be called from a synchronous callback (ErrSyncCallback).
func (h *NodeHost) ExportHostData(address contracts.Address, path string, ctx context.Context) error {
	return h.hostData("exportHostData", &hostDataRequest{Address: wireOf(address), Path: path}, ctx)
}

// ImportHostData imports the JSONL host data archive at path into the process's host under address (Pi's `host.importHostData`
// with `{ address, existing }`; existing is "refuse" or "replace", "" the default). It returns once the commit settled; its error is
// the host's own (a *NodeError holding the ChordError: `invalid/args` for a refused archive or existing data, `unavailable/storage_failed`
// when the commit's outcome is unknown). It must not be called from a synchronous callback (ErrSyncCallback).
func (h *NodeHost) ImportHostData(address contracts.Address, path, existing string, ctx context.Context) error {
	return h.hostData("importHostData", &hostDataRequest{Address: wireOf(address), Path: path, Existing: existing}, ctx)
}

// DeleteHostData deletes the records and rows of the process's host belonging to address in one commit (Pi's `host.deleteHostData`).
// It must not be called from a synchronous callback (ErrSyncCallback).
func (h *NodeHost) DeleteHostData(address contracts.Address, ctx context.Context) error {
	return h.hostData("deleteHostData", &hostDataRequest{Address: wireOf(address)}, ctx)
}

func (h *NodeHost) hostData(op string, body *hostDataRequest, ctx context.Context) error {
	if err := h.refuseSyncCallback(ctx); err != nil {
		return err
	}
	call, err := h.send(op, body)
	if err != nil {
		return err
	}
	_, err = h.await(call, ctx)
	return err
}
