// Package durable ports the root module of Pi's @earendil-works/pi-durable: the durable conversation, task, and
// document runtime types, the ID kinds, the errors, the built-in entry kinds, document and task definitions, and the
// tool-output truncation helpers.
//
// Ports packages/durable/src/index.ts
//
// Go mapping decisions. Each is forced by a Go language mechanic; observable JSON and runtime behavior follow Pi.
//
//   - Chord's Context is context.Context and comes first in every signature. A Promise maps to a blocking call that
//     returns (value, error); a rejection is the error.
//   - JsonValue is any holding strict JSON (nil, bool, float64, string, []any, map[string]any); JsonObject is
//     map[string]any. Chord delta operations are chord/delta Op tuples, and a document Draft[T] is the revocable
//     chord/delta overlay handle (*delta.Object) that records them.
//   - Branded numeric IDs are distinct named int64 types, so a TaskId does not assign to a ConversationId. TaskId does
//     not carry its result type: Go methods cannot take type parameters, so typed results are recovered at the
//     generic helpers (CreateTask, DecodeTaskRecord).
//   - packages/durable/src/types.ts and harness/types.ts import each other. Go packages cannot, so the declarations of
//     harness/types.ts that types.ts references (Agent, Settings, RegistrySnapshot, ToolRegistration, Extension,
//     PromptSection, ContextView, ConversationHandle, Submission, SettledTask, AnyTask, and their closure) live here
//     and durable/harness declares the rest.
//   - Overloaded methods (Tx.doc, Tx.entry, Session.snapshot, createTask, ...) take erased tokens; generic helpers
//     (TxDoc, TxEntry, TxAppendEntry, Snapshot, SnapshotAsOf, CreateTask) restore the value types. Document tokens
//     carry an erased definition over JSON objects; DecodeDoc[T] reads a value or draft snapshot as T.
//   - Discriminated record unions whose members share fields (TaskState, TaskOutcome, SubmissionRecord,
//     DocumentRecord, DocumentContent, ContextEdit) are one struct with a status or kind discriminant; the Session
//     keeps the member fields consistent. Unions of distinct payloads (StorageWrite, CommitChange) are sealed
//     interfaces.
//   - Definition-time TypeErrors (an empty entry kind, a non-positive document version) are panics, as package-level
//     definitions cannot return errors.
package durable
