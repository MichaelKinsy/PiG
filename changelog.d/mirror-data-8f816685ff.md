### Fixed

- The upstream mirror's provider data (`.upstream/current/packages/ai/src/providers/data`) is the snapshot the pin froze for its commit, checked against its digest, and no longer a copy of the installed pi-ai package, which carries the post-release fixes: a mirror made after a fix changed a provider list held the fixed list and the tests that read the mirror failed (`make upstream-mirror` now also ends with the check that every recorded fix still applies to the mirror once).
