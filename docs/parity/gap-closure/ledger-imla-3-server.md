# lg-imla-3: package `server` rows

State: integrate-042 90a40bbe9 (detector with P1(a) only). `make interface-gaps` lists 195 `server` rows. Per-row classes: `labels/rule-requests-imla-3-server.tsv`.

Every `packages/server/test/*.test.ts` file is `ported` in `test-mapping-v1.0.4.json`, with evidence in `internal/experimental/routing` and mutation notes (conformance 20 cases, listener 2, protocol 7, server 6, unix 6, unix-connection 1). `packages/server/src/{types,connection,listener,server,session-router,errors}.ts` and `transports/unix/*` have Go counterparts in `internal/experimental/routing` (with `routingtest` for `src/testing`). No row needs a new Go member:

| class | rows | closing action |
|---|---:|---|
| P1(b) (`not-exercised`) | 136 | `packages/server` is a library whose members Pi exports with no production caller. The members are the public API of `routing` and `routingtest`, none duplicates another member, and each is exercised by a ported Pi test. They close when ledger-autobind lands P1(b). |
| child-gap | 27 | close with their children |
| R2 Context parameter (S4) | 16 | upstream `context: Context` (chord) is the leading `context.Context` of the Go method (`RoutedServerServiceAttachment.Release(ctx)`, `RoutedSessionHandle.Close(ctx)`, `ServerHost.ResolveSession(ctx, id)`, ...). Same rule as lg-help-10 R2. |
| error mechanics | 12 | `cause` and `stack` of `ServerError` and its four subclasses are inherited `Error` members; `code` and `message` are `ServerError.Code` and `.Message` |
| rename | 1 | `SessionMetadata.id` is `SessionMetadata.SessionID()` (`BasicSessionMetadata.ID` is the concrete field) |
| fluent return | 1 | `Server.start(): Promise<this>` returns the receiver; Go `Start() error` leaves the receiver with the caller |
| result resolution | 1 | `createUnixListener` is `CreateUnixListener(UnixListenerOptions) (*UnixListener, error)`; the detector reads the upstream result as nothing and the Go result as string |
| type-level | 1 | `MaybePromise<T>` is `T` (a blocking call) in Go |

No Go code changed in this batch.
