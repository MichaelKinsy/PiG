# Rows of lane N-HTTP (Chord v2 slice 10 HTTP, Pi `6d4b7443`) for the integrator

Branch `fix/follow-nhttp` on `fix/follow-pi-next` @ `65411f70dd`. Delete this file after transcribing. One new Go package `chord/nextnode/http` (package `http`; files `<pi stem>.go`), beside `chord/nextnode` (compression). Upstream: `packages/chord/src/next-node/http/*.ts` at `6d4b7443` (includes `cc08278a`, the no-DOM-types fix: type-level only, the Go form has no DOM types). Unit evidence: `go test -race ./chord/nextnode/http` and `go test -race -run TestHostRootAnonymous ./chord/next/host`. Benchmark: `go test ./chord/nextnode/http -run '^$' -bench HTTP`.

Go-only files (no Pi file): `util.go` (`must`, `recoverAs`, `StringSet`), `url.go` (the WHATWG URL parser's part: dot segments, percent-encode sets, authority checks; Pi calls `new URL`), `statuscodes.go` (Node's `STATUS_CODES`), `websocket.go` (the part of `ws`'s `WebSocket` the component and plugins use, over gorilla), `doc.go`. Tests: `support_test.go` (Pi's `support.ts`), `bench_http_test.go` (Pi's `test/next/bench/http.ts`).

## Design notes (pick-the-conservative-option decisions; review these)

1. **net/http is Node's `http` module.** The listener is a `net/http.Server` with Node's limits in Go's settings (ReadHeaderTimeout 60 s = `headersTimeout`, ReadTimeout 300 s = `requestTimeout`, MaxHeaderBytes 12288 + Go's 4096 request-line allowance = 16 KiB, IdleTimeout 5 s = keep-alive). Forced observable differences, all refusals with another status or status line (node.go header lists them): two `Host` headers and a `Host` with userinfo are answered 400 by net/http before the component runs (Node: the component answers 403); an HTTP/1.0 request's status line says `HTTP/1.0` (Node: `HTTP/1.1`); the reason phrase of the status line is Go's (the answers' own text is Node's table); header names are written in canonical case; a small unread body is drained by net/http (up to 256 KiB) after the handler. The component's own 403 for those requests is kept for `Fetch`/`NodeRequestOf` (a request handed over by any server) and tested there. A hand-written HTTP/1.1 server would match Node's parser exactly; it was not chosen (security-critical parsing belongs to the standard library). **Owner may reject this.**
2. **Router following is an object, not a watch.** Pi's `#follow` watches the router value on its event loop. A Go registry value may be watched or read only inside the host's sections; the watch's abort and `Start` race the host's close from outside (`-race` showed it). The facet serves a host-lifetime local type `chord.http.follower` whose factory does `self.Registry(Http)` + `self.OnNative(router, ...)`; each new router is handed to a single task that terminates the connections it no longer admits (the newest router only: the newest also ends what older ones admitted). It runs from the host's start, not from the first request. `HttpFollower` is that type.
3. **`host.Root` with no principal.** Pi's explicit `principal: undefined` is anonymous; Go's nil is the omitted form. `RootOptions.Anonymous` (host_root.go, 3 lines in `chordHost.Root`; test `host_root_anonymous_test.go`) is the only edit outside the new package. KFold agreed to it.
4. **Context abort is not synchronous in Go.** `AbortSignal.abort()` reaches `ctx.abortSignal` at once; Go links contexts by `AfterFunc` goroutines. `Close` therefore waits for each running request's scope to be seen done after it cancels it (Pi's observable "aborted when `close` returns"); one upstream test (`aborted inside the route`) waits for the link before it reads the scope, with a comment.
5. **Sockets read after their handler returns.** Pi attaches a plugin socket's listeners in the handler's synchronous part before any frame is read. The Go handler returns an error; `WebSocket.Start` begins reading when it returned (frames wait in the connection, not dropped).
6. **Typed Records as ordered slices; functions have no identity.** `routes`/`sockets`/`csp`/response `headers` are ordered slices (conflict-report and header-line order); `HttpSocket` and `HttpRelay` are compared by pointer (Pi: `Set<HttpSocket>`, `relays.includes`).

## Pending upstream tests (exact cases)

All need `host.reload` (slice 7b, K-fold; not in the Go host at `65411f70dd`). Each has a Go twin driven by a writable contribution (the router recomposes the same way; `revoke` is the same code), marked `...ViaAWritable` in `upgrades_test.go`; the originals are to be ported when `Host.Reload` lands:
- `http.test.ts` "a reload swapping a route leaves a running request on the old router; the next uses the new one" (no twin).
- `upgrades.test.ts`: "terminates a plugin's sockets at once when a reload removes its contribution, sealing their scopes; others stay (Q6)"; "terminates links and sockets through a host no longer contributed; loopback ones stay"; "a raw peer whose host or contribution left can send nothing more to the handler"; "checks an upgrade again after authenticating: one whose host left meanwhile is refused".

Designed out (JS mechanics with no Go form): `node.test.ts` "makes a Web Request only when something reads the headers or the body" (counts `new Request`; Go's `*net/http.Request` exists when the server hands it over); `upgrades.test.ts` "keeps no ws server once its socket closed" (spies `WebSocketServer.prototype.handleUpgrade`; the gorilla Upgrader is a stack value per upgrade).

## (a) per upstream file

| path | disposition | PiG files | evidence | rationale |
|---|---|---|---|---|
| packages/chord/src/next-node/http/admission.ts | ported | chord/nextnode/http/admission.go | test:chord/nextnode/http/admission_test.go#TestAdmissionProvenance,TestAdmissionForeignRequestsAndOrigin | `admit` returns `Admission` (`*Admitted` or `*Refused`); optional strings are "" and an absent port 0; `isOrigin` is the URL parser's serialization check written out. |
| packages/chord/src/next-node/http/authenticate.ts | ported | chord/nextnode/http/authenticate.go | test:chord/nextnode/http/authenticate_test.go#TestAuthenticatorsAreAskedInContributorOrderTheFirstPrincipalWinsAThrowingOneIsReportedAndSkipped,TestAuthenticatorsStartNoAuthenticatorOnceTheContextIsAborted,TestAuthenticatorsGiveNoPrincipalWhenNoneKnowsTheRequest | blocking authenticators; nil principal is `undefined`. |
| packages/chord/src/next-node/http/cookies.ts | ported | chord/nextnode/http/cookies.go | test:chord/nextnode/http/cookies_test.go#TestCookiesAreHttpOnlyPathSameSiteStrictByDefaultSecureOnARemoteOriginLaxAndMaxAgeWhenAsked,TestCookiesReadBackOnlyWithTheirSignatureNameAndSecret,TestCookiesRefuseACookiePastItsSignedExpiryAndOneWhoseExpiryWasChanged | `Date.now()` is the package clock `now`; `encodeURIComponent` written out. |
| packages/chord/src/next-node/http/file.ts | ported | chord/nextnode/http/file.go | test:chord/nextnode/http/file_test.go#TestFileResponseAnswersAMissingFile404WithOrWithoutRoot,TestFileResponseWithARootRefusesAFileWhoseRealPathLiesOutsideIt403ServesOneInside,TestFileResponseTypesByExtensionTextImagesAudioVideoNeverHTMLOrSVGUnlessNamed,TestFileResponseClosesItsHandleOnEveryPathOpensNoneWhenNothingIsWritten | handle opened by the first read once, closed by the scope's Done; the descriptor test is Linux-only as in Pi. |
| packages/chord/src/next-node/http/headers.ts | ported | chord/nextnode/http/headers.go | test:chord/nextnode/http/headers_test.go#TestDefaultHeaders,TestDefaultHeadersGC | `WeakMap<Router,...>` is a `weak.Pointer` map with a cleanup; values are Latin-1 text (written as Latin-1 bytes). |
| packages/chord/src/next-node/http/http.ts | ported | chord/nextnode/http/http.go | test:chord/nextnode/http/http_test.go#TestStartupFetchWaitsForThePublicationThenServesTheRegistriesAreResolvedOnce,TestHostAndPrincipalsNeverAuthenticatesAForeignRequestRefusesAForeignOriginAndAnUnsafeBrowserRequestWithoutOne,TestReloadsAndCloseRefusesNewRequests503WaitsForRunningOnesUpToCloseTimeoutThenSealsThem503,TestCancellationReadsNoBytesAfter,TestGCCollectsRequestsScopesAndRequestsOnceAnswered | partial: one upstream test pending on `host.reload` (see above). Design notes 2-4 apply. |
| packages/chord/src/next-node/http/incoming.ts | ported | chord/nextnode/http/incoming.go | test:chord/nextnode/http/admission_test.go#TestAdmissionForeignRequestsAndOrigin,chord/nextnode/http/node_test.go#TestNodeRequestRefusesWhatTheListenerRefuses | one request shape for `Fetch` and the listener. |
| packages/chord/src/next-node/http/index.ts | designed-out | none | n/a | barrel: Go imports the package; exported names are those of Pi's `index.ts`, the rest is unexported. |
| packages/chord/src/next-node/http/node.ts | ported | chord/nextnode/http/node.go, chord/nextnode/http/url.go | test:chord/nextnode/http/node_test.go#TestTargetsAnswerAMalformedOrDoubleSlashPathWithoutCrashingOrReroutingIt,TestMountingConvertsWithTheListenersChecksNeverPanickingBadTargetsUserinfoTRACERefused,TestTheRequestsScopeLastsUntilTheResponseIsFlushedCloseWaitsForIt,TestTheRequestsScopeIsAbortedWhenTheClientDisconnects | design note 1 (net/http as Node's http); one upstream test designed out (lazy Web Request). |
| packages/chord/src/next-node/http/request.ts | ported | chord/nextnode/http/request.go | test:chord/nextnode/http/request_test.go#TestRequestHasItsMethodPathParamsQueryHeadersOriginProvenanceAndNonce,TestBodiesParseJSONWithinItsLimitMalformedIs400LargerIs413,TestBodiesAnswer413BeforeAChunkedUploadPastTheLimitHasEnded,TestBodiesDrainASmallBodyNobodyReadAndKeepTheConnectionALargeOneClosesIt | `body` is an `iter.Seq2` of chunks. |
| packages/chord/src/next-node/http/response.ts | ported | chord/nextnode/http/response.go, chord/nextnode/http/statuscodes.go | test:chord/nextnode/http/response_test.go#TestDescriptionsWriteJSONHTMLRedirectAndErrorTheDefaultHeadersFirstTheResponsesReplacingSameNamedOnes,TestBytesSendContentLengthRead256KiBPiecesAnswerASingleRange206AndAnUnsatisfiableOne416,TestBytesDestroyTheConnectionOnAShortOrAThrowingReadReportedResponseFailed,TestBytesStopReadingWhenTheClientDisconnectsTheReadsContextIsAborted | the pulled `ReadableStream` is `*PulledBody` (an `io.ReadCloser` pulling one piece per Read, ended by the scope's seal). |
| packages/chord/src/next-node/http/router.ts | ported | chord/nextnode/http/router.go | test:chord/nextnode/http/router_test.go#TestRouterPatternsAndPrecedence,TestRouterConflicts,TestRouterWraps,TestRouterHostsAndSockets | design note 6. |
| packages/chord/src/next-node/http/upgrades.ts | ported | chord/nextnode/http/upgrades.go, chord/nextnode/http/websocket.go | test:chord/nextnode/http/upgrades_test.go#TestTheLinksEndpointAcceptsALinkAsTheAuthenticatedPrincipalWithTheOptionsLinksOptionsGaveThatSocket,TestPluginSocketsAThrowingHandlerIsReportedSocketFailedAndClosesItsSocket1011,TestHeartbeatPingsAPluginSocketAfter15sOfSilenceAndTerminatesIt15sLaterWithoutAnAnswer,TestCloseTerminatesASocketThatNeverAcknowledgesItsCloseAtCloseTimeout | partial: four upstream tests pending on `host.reload` (twins `...ViaAWritable` pass); one designed out. Design note 5. |
| test/next/bench/http.ts | ported | chord/nextnode/http/bench_http_test.go | bench:BenchmarkHTTPRequestRoundTrip,BenchmarkHTTPFileThroughput | standard benchmark loop; `plain` against `route`, `pipeline` against `fileResponse`. |

## (b) PORT_MAP rows

| source | PiG | status |
|---|---|---|
| `packages/chord/src/next-node/http/admission.ts` | `chord/nextnode/http/admission.go` | ✅ |
| `packages/chord/src/next-node/http/authenticate.ts` | `chord/nextnode/http/authenticate.go` | ✅ |
| `packages/chord/src/next-node/http/cookies.ts` | `chord/nextnode/http/cookies.go` | ✅ |
| `packages/chord/src/next-node/http/file.ts` | `chord/nextnode/http/file.go` | ✅ |
| `packages/chord/src/next-node/http/headers.ts` | `chord/nextnode/http/headers.go` | ✅ |
| `packages/chord/src/next-node/http/http.ts` | `chord/nextnode/http/http.go` | partial (one upstream test pending on `host.reload`) |
| `packages/chord/src/next-node/http/incoming.ts` | `chord/nextnode/http/incoming.go` | ✅ |
| `packages/chord/src/next-node/http/index.ts` | none | n/a (designed out: barrel) |
| `packages/chord/src/next-node/http/node.ts` | `chord/nextnode/http/node.go`, `chord/nextnode/http/url.go` | ✅ (net/http differences in design note 1) |
| `packages/chord/src/next-node/http/request.ts` | `chord/nextnode/http/request.go` | ✅ |
| `packages/chord/src/next-node/http/response.ts` | `chord/nextnode/http/response.go`, `chord/nextnode/http/statuscodes.go` | ✅ |
| `packages/chord/src/next-node/http/router.ts` | `chord/nextnode/http/router.go` | ✅ |
| `packages/chord/src/next-node/http/upgrades.ts` | `chord/nextnode/http/upgrades.go`, `chord/nextnode/http/websocket.go` | partial (four upstream tests pending on `host.reload`) |
| (Go only) | `chord/nextnode/http/util.go`, `chord/nextnode/http/doc.go` | ✅ |

## (c) upstream tests

| upstream test file | disposition | PiG test | notes |
|---|---|---|---|
| test/next-node/http/admission.test.ts (6 `it`) | ported | admission_test.go: TestAdmissionProvenance (4 subtests), TestAdmissionForeignRequestsAndOrigin (2) | |
| test/next-node/http/authenticate.test.ts (3) | ported | authenticate_test.go: the three `TestAuthenticators...` | |
| test/next-node/http/cookies.test.ts (3) | ported | cookies_test.go: the three `TestCookies...` | fake `Date` is the package var `now` |
| test/next-node/http/headers.test.ts (6) | ported | headers_test.go: TestDefaultHeaders (5), TestDefaultHeadersGC (1) | GC by `weak.Pointer` and bounded `runtime.GC()` |
| test/next-node/http/router.test.ts (12) | ported | router_test.go: TestRouterPatternsAndPrecedence (7), TestRouterConflicts (2), TestRouterWraps (2), TestRouterHostsAndSockets (1) | |
| test/next-node/http/request.test.ts (4) | ported | request_test.go: `TestRequest...`, `TestBodies...` (3) | keep-alive checked with `httptrace` reuse and `Response.Close` |
| test/next-node/http/response.test.ts (10) | ported | response_test.go: `TestDescriptions...` (2), `TestBytes...` (8, one extra: `TestBytesWithoutASizeAreSentChunked`) | status-line reason phrases are Go's |
| test/next-node/http/file.test.ts (4) | ported | file_test.go: the four `TestFileResponse...` | descriptor count Linux-only as upstream |
| test/next-node/http/node.test.ts (7) | partial | node_test.go: `TestTargets...` (3, two-`Host` expectation is 400: design note 1), `TestMounting...`, `TestTheRequestsScope...` (2); `TestFetchRefusesTwoHostHeaders` and `TestNodeRequestRefusesWhatTheListenerRefuses` carry the component's own 403s | "makes a Web Request only when something reads" designed out |
| test/next-node/http/http.test.ts (34) | partial | http_test.go: `TestStartup...`, `TestHostAndPrincipals...`, `TestDefaultHeaders...`, `TestRoutesAndWrapsFailing...`, `TestReloadsAndClose...` (2 of 3), `TestMounting...`, `TestCancellation...`, `TestAnswers...`, `TestGC...` | pending: the reload-swapping test |
| test/next-node/http/upgrades.test.ts (21) | partial | upgrades_test.go (see (a)) | pending: four reload-driven tests (twins pass); designed out: ws-server GC spy |
| test/next/bench/http.ts | ported | bench_http_test.go | |
| test/next-node/http/support.ts | ported | support_test.go | helpers |

## (d) async-contract rows

| file | contract | evidence test | rationale |
|---|---|---|---|
| http.ts `fetch`/`#serve` (async; `Promise.withResolvers` answered/finished; `host.root` not awaited) | awaited answer; the request's scope is an owned goroutine of `Root`, joined by `running.done` | TestReloadsAndCloseRefusesNewRequests503WaitsForRunningOnesUpToCloseTimeoutThenSealsThem503, TestGCCollectsRequestsScopesAndRequestsOnceAnswered | `answered` is a buffered channel, `finished` a once-closed channel; `close` waits for `done` then cancels and waits for each scope's seal. |
| http.ts `#principal` (`awaitWithContext(authenticating)`) | awaited, cancellable | TestCancellationCloseSettlesARequestWhoseAuthenticatorIgnoresItsSignal503 | authenticators run in an owned goroutine; the caller selects on the result and the request's context. |
| http.ts `#resolve` (`Promise.all` of two `host.registry`, retried after a failed try) | awaited, joined | TestStartupFetchWaitsForThePublicationThenServesTheRegistriesAreResolvedOnce | one resolution task; waiters block on its done channel; a failure clears it. |
| http.ts `#follow` (unawaited watch, `.catch(() => {})`) | owned background task | TestRevocation...ViaAWritable (upgrades_test.go) | design note 2: an `OnNative` routine of the follower object; revocation runs on one task, newest router only. |
| http.ts `#upgrade` (async; stream `close`/`end` listeners) | awaited, cancellable | TestCancellationAbortsAnUpgradesAuthenticationWhenItsClient | the request's context ends on FIN and RST (net/http's background read). |
| http.ts `#close` (`Promise.race([Promise.all([upgrades.close, running]), deadline])`) | awaited, bounded | TestCancellationACloseWhoseContextIsAbortedAlreadyDoesNotWait, TestCloseTerminatesASocketThatNeverAcknowledgesItsCloseAtCloseTimeout | `deadline` is a once-closed channel (timer or the context's abort). |
| node.ts `write`/`pump` (backpressure `drain`, `res.once("close")`) | blocking write loop | TestTheRequestsScopeLastsUntilTheResponseIsFlushedCloseWaitsForIt | `ResponseWriter.Write` blocks as the socket drains; the scope's `finished` is a defer. |
| response.ts `pulled` (`ReadableStream` pull, seal handler) | pull per Read, cancellable | TestBytesStopReadingWhenTheClientDisconnectsTheReadsContextIsAborted | `read` runs in a goroutine selected against the scope's Done; a late chunk is dropped. |
| file.ts `read` (`handle ??= opened(...)`) | once-opened handle | TestFileResponseClosesItsHandleOnEveryPathOpensNoneWhenNothingIsWritten | `sync.Once`; closed by `context.AfterFunc` on the scope. |
| upgrades.ts `socket` (`void host.root(...)`) | owned background task | TestPluginSocketsAThrowingHandlerIsReportedSocketFailedAndClosesItsSocket1011, TestGCKeepsNothingOfAClosedPluginSocket | ends when the socket closes. |
| upgrades.ts `close(deadline)` | awaited, bounded | TestCloseTerminatesASocketThatNeverAcknowledgesItsCloseAtCloseTimeout | |

## (e) new public interface IDs

All of package `chord/nextnode/http`: `CreateHttp`, `HttpComponent`, `HttpOptions`, `LinksOptions`, `HttpError` (+ kinds), `HookName` (+ consts), `HttpListener`, `HttpFollower`, `Http`, `Authenticators`, `Authenticator`, `AuthenticatorEntry`, `ComposeRouter`, `Router`, `RouteMatch`, `SocketMatch`, `ResolvedWrap`, `Lookup` (+ `Found`, `NoMatch`, `BadPath`), `HttpRoute`, `HttpRouteOptions`, `HttpRouteEntry`, `RouteFunc`, `WrapPhase` (+ consts), `HttpWrap`, `HttpSocket`, `HttpSocketEntry`, `HttpRelay`, `RelayVerified`, `CspEntry`, `HttpContribution`, `UNCARRIED`, `HttpRequest`, `HttpFailure`, `NewHttpFailure`, `HttpResponse`, `ResponseKind` (+ consts), `ResponseHeader`, `HeaderOf`, `Answer`, `PulledBody`, `ResponseFailure`, `FileResponse`, `FileResponseOptions`, `Cookie`, `CookieOptions`, `ReadCookie`, `Provenance`, `ProvenanceKind` (+ consts), `Connection`, `Admission`, `Admitted`, `Refused`, `RelayFailure`, `AnswerHeaders`, `NewAnswerHeaders`, `HeaderError`, `HeaderLine`, `DefaultHeaders`, `NewDefaultHeaders`, `Nonces`, `NewNonces`, `Incoming`, `WebIncoming`, `HeaderGet`, `ListenAddress`, `Served`, `Handler`, `NodeListener`, `ListenNode`, `NodeRequest`, `NodeRequestOf`, `WireRefusal`, `StringSet`, `NewStringSet`, `Upgrades`, `NewUpgrades`, `UpgradeAdmission`, `LinkOptions`, `SocketFailure`, `WebSocket`, `WebSocketHandle`, `CloseEvent`, WebSocket state and event consts. Also `chord/next/host`: `RootOptions.Anonymous`.

## Open questions

1. Design note 1: accept net/http's refusals (400 for two `Host`s and a `Host` with userinfo, `HTTP/1.0` status line) or write a Node-faithful parser? A divergence record may be needed if accepted.
2. `Host.Load` returns `(LoadReport, error)` after K-fold: `served()` in `support_test.go` and `bench_http_test.go` call it as a single value (mechanical rewrite with `mustLoad`).
3. The five reload tests above, once `Host.Reload` has landed.
