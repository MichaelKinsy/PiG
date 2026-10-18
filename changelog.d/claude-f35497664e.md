### Changed

- Go SDK `chord/host`: `Method` takes its schemas variadically like Pi's `method()` overloads (`Method()`, `Method(args)`, `Method(nil, result)`, `Method(args, result)`), and `PlacementFor` and `PrincipalPattern` are exported for Pi's types of the same names. Existing two-argument calls compile unchanged.
