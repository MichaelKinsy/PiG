// Package writes normalizes durable storage writes for the storage backends.
package writes

import "github.com/MichaelKinsy/PiG/durable"

// Value returns the value form of a write passed by pointer, so backends switch on value types only.
func Value(write durable.StorageWrite) durable.StorageWrite {
	switch typed := write.(type) {
	case *durable.ConversationWrite:
		return *typed
	case *durable.EntryWrite:
		return *typed
	case *durable.TaskWrite:
		return *typed
	case *durable.SubmissionWrite:
		return *typed
	case *durable.DocumentCreateWrite:
		return *typed
	case *durable.DocumentCopyWrite:
		return *typed
	case *durable.DocumentChangeWrite:
		return *typed
	case *durable.DocumentRetireWrite:
		return *typed
	default:
		return write
	}
}

// Values returns the value forms of a batch.
func Values(batch []durable.StorageWrite) []durable.StorageWrite {
	out := make([]durable.StorageWrite, len(batch))
	for index, write := range batch {
		out[index] = Value(write)
	}
	return out
}
