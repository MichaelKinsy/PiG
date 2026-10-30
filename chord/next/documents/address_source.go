package documents

import (
	"context"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/state"
)

// Ports packages/chord/src/next/documents/address-source.ts
//
// Watchable document addresses (slice 6a §11; decision "One retirement mechanism"): the frames of a source that
// follows one address, a singleton's slot or one key of a keyed slot. A change arrives with its ops, a write over
// another base as a root replacement, a retirement as `[["r", initial()]]` with the `retired` flag, and a document
// created after a retirement in the same batch as one root replacement with the flag.
//
// Go form of the abstract class: AddressSource embeds state.Source and is embedded by the concrete source, whose Init
// gives it itself as AddressImpl (`owner` and `value` are the embedder's).

// AddressImpl is what an AddressSource needs of its embedder: the documents of the object whose document this is (a
// member source asks whose source it follows) and the value (`initial()` while the document is absent).
type AddressImpl interface {
	state.SourceImpl
	// Owner is the documents of the object whose document this is.
	Owner() *Documents
}

// AddressSource is a source following one document address.
type AddressSource struct {
	state.Source
	// ops are the ops of the current batch's frame, kept only while watched.
	ops     []delta.Op
	retired bool
	impl    AddressImpl
}

// Init makes the source of batch, impl its embedder.
func (source *AddressSource) Init(batch *state.Batch, impl AddressImpl) {
	source.Source.Init(batch, impl)
	source.impl = impl
}

// Deliver publishes this batch's frame: the value, the ops and whether the document was retired in it.
func (source *AddressSource) Deliver(ctx context.Context) error {
	value, err := source.impl.Value()
	if err != nil {
		return err
	}
	ops, retired := source.ops, source.retired
	source.ops, source.retired = nil, false
	source.Publish(value, nonNilOps(ops), retired, ctx)
	return nil
}

func nonNilOps(ops []delta.Op) []delta.Op {
	if ops == nil {
		return []delta.Op{}
	}
	return ops
}

// Written is: a commit wrote the document, which read `before`: its ops, or a root replacement when the draft started
// from another base or the document was retired earlier in the batch.
func (source *AddressSource) Written(before *JsonObject, prepared *delta.Prepared) {
	// The ops are kept for a frame, which only a watched source gets: an unwatched one keeps none.
	if source.Watched() {
		if prepared.Base() == before && !source.retired {
			source.ops = prepared.Ops()
		} else {
			source.ops = []delta.Op{{"r", prepared.Value()}}
		}
	}
	source.Changed()
}

// RetiredTo is: the document was retired: it reads `initial` again, and the frame says so.
func (source *AddressSource) RetiredTo(initial *JsonObject) {
	// Reset by the frame, which only a watched source gets.
	if source.Watched() {
		source.ops = []delta.Op{{"r", initial}}
		source.retired = true
	}
	source.Changed()
	source.RetiredInBatch()
}

// Replaced is: the value changed outside a commit (a migration, a load): a root replacement.
func (source *AddressSource) Replaced() error {
	if source.Watched() {
		value, err := source.impl.Value()
		if err != nil {
			return err
		}
		source.ops = []delta.Op{{"r", value}}
	}
	source.Changed()
	return nil
}
