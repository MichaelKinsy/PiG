package session

import (
	"context"
	"fmt"

	"github.com/MichaelKinsy/PiG/durable"
)

// Ports packages/durable/src/session/forks.ts

const forkScanPageSize = 256

// forkDocumentCopy is one definition-free document copy to create with a
// forked conversation.
type forkDocumentCopy struct {
	record durable.DocumentCreate
	source durable.DocumentCopySource
}

// prepareForkDocumentCopies selects every persisted conversation document
// copied by one fork: as-of documents of the entry-owning ancestor at the
// entry's commit, then current documents of the immediate parent.
func prepareForkDocumentCopies(ctx context.Context, storage durable.Storage, parentConversationId durable.ConversationId, at durable.EntryId, childConversationId durable.ConversationId) ([]forkDocumentCopy, error) {
	entry, err := storage.VisibleEntry(ctx, parentConversationId, at)
	if err != nil {
		return nil, err
	}
	if entry == nil {
		return nil, fmt.Errorf("Entry %d is not visible from conversation %d", at, parentConversationId)
	}
	var copies []forkDocumentCopy
	copiedAddresses := map[string]bool{}
	asOfScope := durable.DocumentRecordScope{Kind: durable.ScopeConversation, ConversationId: entry.Entry.ConversationId}
	if err := collectCopies(ctx, storage, asOfScope, durable.AtSeq(entry.CommitSeq), durable.ForkAsOf, childConversationId, &copies, copiedAddresses); err != nil {
		return nil, err
	}
	currentScope := durable.DocumentRecordScope{Kind: durable.ScopeConversation, ConversationId: parentConversationId}
	if err := collectCopies(ctx, storage, currentScope, durable.CurrentPoint, durable.ForkCurrent, childConversationId, &copies, copiedAddresses); err != nil {
		return nil, err
	}
	return copies, nil
}

func collectCopies(ctx context.Context, storage durable.Storage, scope durable.DocumentRecordScope, at durable.DocumentPoint, policy durable.DocumentFork, childConversationId durable.ConversationId, copies *[]forkDocumentCopy, copiedAddresses map[string]bool) error {
	var cursor durable.Cursor
	for {
		page, err := storage.ScanDocuments(ctx, durable.DocumentQuery{Scope: scope, At: at}, forkScanPageSize, cursor)
		if err != nil {
			return err
		}
		for _, source := range page.Items {
			if source.Scope.Kind != durable.ScopeConversation || source.Fork != policy {
				continue
			}
			id, err := storage.MintId()
			if err != nil {
				return err
			}
			record := durable.DocumentCreate{
				Id:      durable.DocumentId(id),
				Kind:    source.Kind,
				Key:     source.Key,
				Scope:   durable.DocumentRecordScope{Kind: durable.ScopeConversation, ConversationId: childConversationId},
				History: source.History,
				Fork:    source.Fork,
			}
			copyAddress := durable.AddressId(durable.DocumentAddress{Kind: record.Kind, Scope: record.Scope, Key: record.Key})
			if copiedAddresses[copyAddress] {
				member := record.Kind
				if record.Key != nil {
					member = record.Kind + "/" + *record.Key
				}
				return fmt.Errorf("Fork selects multiple source documents for %s", member)
			}
			copiedAddresses[copyAddress] = true
			*copies = append(*copies, forkDocumentCopy{record: record, source: durable.DocumentCopySource{Id: source.Id, At: at}})
		}
		if page.Next == nil {
			return nil
		}
		cursor = *page.Next
	}
}
