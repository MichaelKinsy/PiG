### Changed

- Tooling: `make test-carried-oracles`, which runs at another Pi commit (`PI_UPSTREAM_REF`), points `.upstream/current` back at the pin's mirror when it ends (`pin.py point-current`). Before, the link stayed at the carried commit, and the next `make interface-gaps` read that Pi and reported its reopened rows as seven gaps of the tree. A failed `make interface-gaps` over a mirror that is not the pinned build now says so and names `make upstream-mirror`.
