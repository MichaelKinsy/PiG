# Release reliability

## Read first

- `../docs/project/CONTEXT.md`
- `../docs/project/RELEASING.md`
- `../docs/project/PROVENANCE.md`
- `../SECURITY.md`

## Contract

Stock PiG artifacts and PiG Standard Piglet release artifacts are distinct. Each artifact identifies one source commit, target, toolchain, and composition. Published bytes equal tested and attested bytes. Publication requires explicit approval.

## Failure modes

| Failure | Detection | Required result |
|---|---|---|
| The source archive omits intended source | Compare `git archive HEAD` with the reviewed manifest | Match exactly |
| An artifact differs from tested bytes | Recompute its digest before publication | Stop publication |
| Stock activates Standard behavior | Run Stock without selected Resources | Keep Stock product-neutral |
| An archive crosses its extraction root | Test traversal, links, devices, and overwrite | Reject the archive |
| An update replaces a valid binary with incomplete data | Truncate and corrupt downloads | Verify before replacement |
| Public evidence exposes private context | Scan paths, domains, credentials, logs, and embedded assets | Publish no private value |
| A target is advertised without native proof | Install and smoke the release artifact on that target | Advertise only passed targets |
| One composition reuses another composition's evidence | Compare archive, SBOM, provenance, and signature identities | Keep evidence separate |

## Evidence

Build from `git archive HEAD` in an empty workspace. Test each composition and target through its public install and execution path. Produce separate checksums, SBOMs, provenance, signatures, and smoke logs. Record toolchain and source identity. After an external release backup and restore procedure is approved, test restoration before publication. Do not commit, tag, change visibility, or publish without explicit approval.
