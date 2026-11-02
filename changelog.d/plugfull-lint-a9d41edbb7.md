### Fixed

- `chord/nextnode/plugins`: a failed build's empty `errors` list reaches the Node host as `[]` as in Pi, and a proxy script for a Go core facet whose name has `<`, `>` or `&` is written as `JSON.stringify` writes it. <!-- fix: a9d41edbb7 -->
