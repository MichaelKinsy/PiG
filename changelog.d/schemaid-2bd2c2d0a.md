### Fixed

- A schema whose root `$id` is a relative path with a directory, such as `schemas/tool.json`, resolves its local `$ref`s: the `$id` was applied twice, so compiling failed with `Unresolved $ref` and tool arguments were rejected under `UnresolvedRefsReject`. The same holds with `unevaluatedProperties` and `unevaluatedItems`, when explaining errors under nested resources and on the first coercion of a schema with `$dynamicRef`. Follows Pi main 2bd2c2d0a ahead of the pin (D302).
