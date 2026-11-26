### Fixed

- Pre-flight `gitleaks` now runs from inside the added-lines directory (`dir .`) instead of passing an absolute temporary directory path, so `^`-anchored `paths` allowlist entries in `.gitleaks.toml` and relative fingerprints in `.gitleaksignore` match repository-relative paths. <!-- fix: ca588df48a --> <!-- cause: automation/ci/preflight/config.yaml passed absolute {input} to gitleaks dir, preventing ^-anchored allowlist paths and relative .gitleaksignore fingerprints from matching -->
