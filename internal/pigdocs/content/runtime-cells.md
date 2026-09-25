# Runtime cells

Runtime cells are Pig's internal process-packing optimization. Authors select a
conventional factory or an exact standalone. Authors do not configure cells.

Pig can pack Go, Rust, and Python factories by language. Node factories and all
standalones run in isolated processes. Each packed extension keeps one socket
and one registration handshake. Packed behavior must match isolated behavior.

Pig derives a cell cache key from source content, SDK content, generated runner
content, composition, language runtime, and toolchain. `/reload` reuses an exact
cache hit and rebuilds changed input.

A shared-process exit quarantines that cell. One stalled handler or one logical
member failure does not quarantine healthy siblings. The next reload fissions a
quarantined composition into isolated processes.
