# PiG fork of aws-sdk-go-v2 (endpoint partition regexps compiled on first use)

These directories are non-test sources of the following `github.com/aws/aws-sdk-go-v2` modules at the versions pinned before the fork, copied from the module proxy (Apache-2.0, `LICENSE.txt`, `NOTICE.txt`): `config` v1.32.17 (+ its `internal/ini`), `credentials` v1.19.16 (`credentials`, `logincreds`, `ssocreds`, `stscreds`), `service/sts` v1.42.1, `service/sso` v1.30.17, `service/ssooidc` v1.35.21, `service/signin` v1.0.11, `service/bedrockruntime` v1.50.6 (+ `document`, `types`, `internal/document`), and the `internal` packages of `aws-sdk-go-v2` v1.47.1 and of the service modules they import (`internal/auth`, `internal/configsources`, `internal/endpoints`, `internal/endpoints/awsrulesfn`, `internal/endpoints/v2`, `internal/sdk`, `internal/shareddefaults`, `internal/v4a`, `service/internal/accept-encoding`, `service/internal/presigned-url`). They are packages of the root module, `github.com/MichaelKinsy/PiG/third_party/awssdk/...`; a `replace` directive would make `go install github.com/MichaelKinsy/PiG/cmd/pig@latest` fail. Go's `internal` rule forces the internal packages to be copied together with the packages that import them.

Each service's `internal/endpoints` compiled eight partition regular expressions in a package initializer (five services: about 265 KB and 2,430 allocations at `pig --version`), whether or not the process called AWS. The fork compiles each expression on first match.

## Differences from upstream

1. Import paths of every copied package (and of PiG's `ai` files and parity fixtures that used them) point at the fork. Packages that are not copied (`aws`, `aws/retry`, `aws/signer/v4`, `credentials/{ec2rolecreds,endpointcreds,processcreds}`, `feature/ec2/imds`, `aws/protocol/*`, smithy-go) are still the upstream modules.
2. `internal/endpoints/v2/lazy.go` (new): `LazyRegexp` / `NewLazyRegexp(expr)` compile `expr` with `regexp.MustCompile` on the first `MatchString` (`sync.Once`). `lazy_test.go` (new) checks it matches like `regexp.Regexp`.
3. `internal/endpoints/v2/endpoints.go`: `Partition.RegionRegex` is `*LazyRegexp`.
4. `service/{sts,sso,ssooidc,signin,bedrockruntime}/internal/endpoints/endpoints.go`: `partitionRegexp` fields are `*endpoints.LazyRegexp` built with `endpoints.NewLazyRegexp`. The expressions are unchanged.
5. `config/env_config.go`: one `%q` verb for an `int64` became `%d` so `go vet` passes (message text otherwise identical for the value, without quotes).
6. Tests, `go.mod`, `go.sum`, `doc.go`, `CHANGELOG.md` and `generated.json` are not copied. The root `go.mod` drops `config`, `service/{sts,sso,ssooidc,signin,bedrockruntime}`, `internal/{configsources,endpoints/v2,v4a}` and `service/internal/*` requirements (nothing else imports them); `aws-sdk-go-v2`, `credentials` (ec2rolecreds, endpointcreds, processcreds), `feature/ec2/imds` and smithy-go stay.
7. `internal/endpoints/awsrulesfn` keeps its `regexp.MustCompile` inside `GetPartition` (a function, run per resolution), unchanged.

## Updating

Bump the versions in the root `go.mod` only for the upstream modules still required, fetch the new release of each copied module, copy the same directories, repeat differences 1-5, re-add any new `internal` package the copied packages import, and run `go build ./...`, `go vet ./third_party/awssdk/...`, `go test ./third_party/awssdk/... ./ai -run 'Bedrock|Endpoint'`. Offer nothing upstream without the owner.
