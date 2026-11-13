# Cloudflare host of the Durable core

`npm ci` in this directory installs the Pi packages the host and its pi-durable facade run on (`@earendil-works/pi-durable`, `chord`, `pi-ai`, `pi-telemetry` and their `pi-codemode` and `pi-schema`) from `third_party/pi-packs`: the `npm pack` outputs of the Pi build PiG pins (`.upstream/pin`), not the release of the same version number on the npm registry (a commit pin has no published package, and the release lacks what the pin has). `package.json` names each pack by a `file:` path, the lock holds its sha512, and `make pin-check` fails when a pack, the lock or a consumer drifts from the pin (`automation/pin/vendor.py`; `docs/parity/upstream-pin.md`). Only the host's other dependencies come from the registry.

`./check.sh` type-checks the host and runs its tests against the core built with TinyGo and Go.
