### Added

- `pig piglet build --format binary` and `pig piglet publish` build a Piglet that lists no extensions. A base that only strips built-ins and sets defaults, such as a `model`, becomes a Binary of PiG's own parts with that Piglet baked in, and a child that extends it can add extensions. A Piglet that lists extensions that resolve to none still fails with `piglet resolves to no extensions`.

### Changed

- A Piglet can't strip every built-in tool and `/quit` together. `pig piglet validate`, `pig piglet build` and `pig --piglet` refuse such a strip list, also when it comes together through `extends`, and name the entries: keep at least one tool or `/quit`. Stripping `/quit` alone (Ctrl+D and a double Ctrl+C still exit) or every tool alone (a chat-only Piglet) stays allowed.
