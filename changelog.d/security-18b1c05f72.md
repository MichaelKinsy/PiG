### Security

- PiG's own TLS connections refuse a certificate chain that includes one of the eight eMudhra intermediate CAs whose private keys anyone can recover from their public certificates (Mozilla bug 2083449), or one of the three roots Mozilla removed from NSS 3.131.1 (`emSign ECC Root CA - G3`, `emSign Root CA - C1`, `emSign ECC Root CA - C3`). Sectigo's cross-signed certificates of the same keys are refused too. This covers model providers, OAuth, MCP, package and tool downloads, self-update, the Codex WebSocket and Bedrock. Recorded as D191.
- CI, the parity image and the devcontainer move to Node.js 24.22.1 (current 26.12.1), the 2026-10-21 security releases (CVE-2026-105482, CVE-2026-105483, CVE-2026-106069, CVE-2026-111497 and others). PiG ships no Node.js binary: update the Node.js that runs your TypeScript and JavaScript extensions to 22.23.4, 24.22.1, 26.12.1 or later.

### Fixed

- A PiG TLS client no longer accepts a certificate chain through a compromised eMudhra intermediate or a root Mozilla removed after it; the error names the CA. <!-- fix: 18b1c05f72 -->
- The CI images no longer ship the three removed emSign roots: `ca-certificates` 20261020-r0 in the ci-go lock, 20260909-r3 and a newer Wolfi base (glibc 2.44) in the ci-parity lock, which also takes `c-ares` 1.34.9 and `nodejs-24` 24.22.1. <!-- fix: 18b1c05f72 -->

### Known issues

- Node.js 24.22.1 orders the microtasks of `values().next()` after a buffered byte-stream read differently from 24.19.0 (`values-buffered/bytes`); the Web Streams models in `ai` still replay 24.19.0 (`ai/testdata/node-microtasks/oracle-node-version`).
