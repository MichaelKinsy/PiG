### Changed

- Tooling: a failed `make interface-gaps` over a mirror that is not the pinned build now says so and names `make upstream-mirror`. Before, a `.upstream/current` left at another Pi commit (as a `make test-carried-oracles` run left it at the time) made the check read that Pi and report its reopened rows as seven gaps of the tree.
