### Changed

- The Cloudflare Durable host's TypeScript tests type-check and run against Pi's chord, pi-ai and pi-durable from the build of the commit `.upstream/pin` names (the published release when the pin is a tag), not against the npm release of the locked version, so they follow Pi when the pin is a commit after a release. `make durable-core-host-deps` links the pinned build and `check.sh` refuses a tree that does not use it.
- The Cloudflare host's pi-durable facade has `Harness.wake()` and `Harness.retryTask()` as Pi's `Harness` does, and passes the telemetry span in a provider's stream options, between `signal` and `sessionId`, as Pi's generation does.
