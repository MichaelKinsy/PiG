### Changed

- Chord v1 (`chord/host`) validates with the `schema` package instead of the TypeBox-era engine, as Pi does since the switch to pi-schema. A `Schema` is a `*schema.Object` that keeps the order its members were declared in; a failed check's `Details` are `{path, keyword, message}` per error (the first three), with pi-schema's messages (`state thing: / expected string, got integer`). `format` is an annotation: no format is checked. Schemas a peer sends in a contract token are stripped of unknown fields like any other (before, a decoded token's schemas stripped nothing). A frame is checked against the frame schema compiled once, at 110 to 840 ns and no allocation per frame (before: 5 to 34 µs and 48 to 335 allocations).
- A host a route resolves is admitted before the route authorizes again and the request's currency is checked, so a worker spawned for a relayed hold that was released, refused or revoked meanwhile still has its link read; its own references to its parent no longer wait until they fail with `unavailable/ready_timeout`.

### Removed

- `Description`'s unexported order lists and `Contract.decoded`: a decoded schema keeps its order itself.
