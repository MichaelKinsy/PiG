set +e
r() { n=$1; shift; echo "=== $n"; "$@" > /tmp/g-$n.log 2>&1; rc=$?; echo "RESULT $n rc=$rc"; [ $rc -ne 0 ] && tail -25 /tmp/g-$n.log; true; }
git fetch -q origin main batch/0.6.0-next 2>&1 | tail -2
git rev-parse origin/main origin/batch/0.6.0-next
r build go build ./...
r vet make vet
r lint-changed-batch make lint-changed LINT_BASE=origin/batch/0.6.0-next
r docs-drift make docs-drift
r changelog-check make changelog-check
r bug-ledger-check make bug-ledger-check
r divergence-consistency make divergence-consistency
r source-hygiene make source-hygiene
r portmap-check make portmap-check
r module-size make module-size
r sdk-exports-release go run ./test/parity/cmd/sdksurface -check -release
r govulncheck go tool govulncheck ./...
r deadcode sh -c 'go tool deadcode ./... | tee /tmp/dc.out; test ! -s /tmp/dc.out'
r gofix sh -c 'go fix -diff ./... | tee /tmp/fix.out; test ! -s /tmp/fix.out'
r reuse reuse lint
r binsize go test ./test/perfgate -run 'TestReleaseBinarySizeGate|TestHeadlessEmbedBinarySizeGate' -count=1 -v
r lint-changed-main make lint-changed LINT_BASE=origin/main
r divergence-delta ./automation/ci/check-divergence-delta.sh
r coverage-delta-unit python3 -B -m unittest discover -s automation/ci -p test_check_coverage_delta.py
r coverage-delta ./automation/ci/check-coverage-delta.sh
r sdk-apidiff make sdk-apidiff
r changelog-gate-appversion go test ./test/gomodule -count=1
echo done
