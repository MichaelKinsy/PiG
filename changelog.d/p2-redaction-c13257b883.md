### Fixed

- p2: log redaction no longer sorts and percent-encodes every known secret and re-matches every environment name for each line; a worker's stack dump (hundreds of stderr lines) is redacted in a fraction of the time, so a failed test's report no longer misses the stacks when the machine is loaded. <!-- fix: c13257b883678bbf456b07c6530830122290c251 -->
