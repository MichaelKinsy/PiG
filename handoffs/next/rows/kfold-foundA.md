# K-fold foundA rows (contracts, data, link, registries, state, documents)

## (a) upstream src files
chord/next/contracts/accepts.ts | ported | contracts/accepts.go | test:chord/next/contracts/accepts_test.go#TestAcceptsKeyedMappings | KeyedMapping, VersionMappings.Keyed, needs.keyed, checkKeyedMapping
chord/next/contracts/contract.ts | ported | contracts/contract.go (VariantsAt) | test:contracts/contract_test.go#TestVariantsAt (PiG-own; no upstream test) | variantsAt; unknown version is an error (TS `!`)
chord/next/contracts/evolve.ts | ported | contracts/evolve.go | test:contracts/evolve_test.go#TestEvolveKeyedEdits | LocalEditor.Keyed/AddKeyed/RemoveKeyed; draft clones Keyed; refuseDeclared sees keyed; type-level PutKeyed/DropKeyed designed out
chord/next/contracts/history.ts | ported | contracts/history.go | SideKeyed via the two tests above | Change side "keyed"
chord/next/contracts/internal.ts | designed-out | - | - | barrel; Go imports the package
chord/next/data/args.ts | ported | data/args.go | test:data/args_test.go#TestBinds (PiG-own) | Provider.Accepts, Binds
chord/next/data/internal.ts | designed-out | - | - | barrel
chord/next/link/binary.ts, text.ts, message.ts | ported | link/{binary,text,message}.go | test:link/message_test.go#TestMessages,TestTheBinaryCodec,TestTheTextCodec; test:link/oracle_test.go#TestWireFormatsMatchPi (golden regenerated from Pi T with link_wire.mjs; only retry entries changed) | RetryMessage{Hops}, RETRY_HOPS, schema 1..16
chord/next/link/internal.ts | designed-out | - | - | barrel
chord/next/registries/contribution.ts | ported | registries/contribution.go | test:registries/record_test.go#TestAReloadsNewContributionAdapter | adapter identity in adapt cache; Reads()/cycle edge removed
chord/next/registries/record.ts | ported | registries/record.go | test:registries/record_test.go#TestTheTable (ring tests), #TestProviderCheck | CheckProvider, Serve (no error, replaces, batch), Unserve, ComposeServed; RegistryEdge/AssertNoCycle/reads removed
chord/next/registries/value.go (value.ts) | ported | registries/value.go | test:registries/value_test.go#TestAReloadsNewValueAdapter | mapped/mapFailure keyed by adapter; Record() removed
chord/next/registries/internal.ts | ported | registries/internal.go | - | sameFunc helper (JS === on adapters, closure-pointer compare via unsafe)
chord/next/state/batch.ts | ported | state/batch.go | test:state/batch_own_test.go#TestBatchCount | CounterContractAdapts
chord/next/state/watchable.ts | unchanged-observable | state/watchable.go | - | lazy watch/subscriber sets: Go orderedSet zero value is already lazy; doc comments updated. Exports needed by host reference-source (Batch, Dependent, DeriveSource, HandedOut, Source, Subscriber) already exist.
chord/next/documents/address-source.ts, keyed-slot.ts | ported | documents/address_source.go, keyed_slot.go | test:documents/address_source_test.go | AddressImpl.Owner and KeySource.Owner removed (SingletonSlot keeps its own owner)
chord/next/documents/documents.ts | ported | documents/documents.go | test:documents/documents_test.go#TestDocumentsCheckChecksADeclarationAsDeclareWouldChangingNothing | Documents.Check
chord/next/documents/transaction.ts | ported | documents/transaction.go | covered by existing transaction tests | handle() calls store TableHandle.AssertVersion

## (c) upstream tests
contracts/accepts.test › keyed mappings (slice 7b) | ported | TestAcceptsKeyedMappings
contracts/evolve.test › keyed edits (2 cases) | ported | TestEvolveKeyedEdits
documents/address-source.test (owner removed) | ported | address_source_test.go (testAddress.Owner dropped)
link/message.test › EVERY_KIND retry cases, binary retry bytes, text retry tuple, refuses retry without hops | ported | link/message_test.go
registries/record.test › ring at first composition; later recompositions bounded | ported at record level | TestTheTable subtests
registries/record.test › ring through a derive / within one factory | pending host (needs attempts, self.derive, host.get)
registries/record.test › removed cycle-at-contribute cases | designed-out (removed upstream); Go twin removed
registries/record.test › reload's new contribution adapter | ported at record level (Serve replaces provider) | TestAReloadsNewContributionAdapter (host.reload part pending host lane)
registries/value.test › reload's new value adapter | ported at record level | TestAReloadsNewValueAdapter
registries/registry.test › provider without compose | CheckProvider ported | TestProviderCheck; "second serve leaves plugin out at load" (host.load failed[]) pending host lane
test/next/support.ts (loaded/loadedPlugins failed, relayed route) | host test support, not in my packages

## (d) async contracts
none (packages are synchronous; link codecs unchanged in contract).

## (e) new exported identifiers
contracts: KeyedMapping, VersionMappings.Keyed, LocalEditor.Keyed/AddKeyed/RemoveKeyed, SideKeyed, VariantsAt
data: Provider.Accepts, Binds
link: RETRY_HOPS, RetryMessage.Hops (Operation, Through removed)
registries: CheckProvider, RegistryRecord.Unserve; Serve now returns nothing; removed RegistryEdge, AssertNoCycle, RegistryContribution.Reads, RegistryValueSource.Record
state: CounterContractAdapts
documents: Documents.Check; removed AddressImpl.Owner, KeySource.Owner

## Host call-site edits (minimal, to compile)
remote_consumer.go retry sends Hops=RETRY_HOPS (no op id on the wire); remote_hold.go retry handler makes a new retryOperation; host_members.go contributionOf lost `pending`; host_attempt.go lost pendingEdges/AssertNoCycle at contribute; host_host.go serveEntry does double-serve + CheckProvider then Serve; tests: remote_relay_test, remote_hold_test, remote_remote_object_test (Hops instead of Operation), host_retry_on_test (Serve no error), host_members_test (contributionOf args).
Host tests asserting old semantics (to be reworked by host lane): host_members_test contribution refusing own registry value (invalid/cycle at contribute); registry cycle-at-contribute cases; cross-host retry operation/through dedupe (host_object_test diamond, ring) and `owns` for key sources (KeySource.Owner removed).

## Open questions
- sameFunc uses unsafe closure-pointer identity; RegistryProvider.Erase makes fresh closures per call, so re-serving an identical typed provider remaps (JS would not).
- Red evidence: new APIs absent => compile errors (see /tmp/kfold/red-foundA-contracts.txt, red-foundA-link.txt); registries tests written with their API change.
