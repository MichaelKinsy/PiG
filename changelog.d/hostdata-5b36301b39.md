### Fixed

- A Chord activation that waited at the host data gate while an import or delete ran no longer crashes the host when its object ended meanwhile (a reload that no longer serves the type, a close). Its documents were gone, and the activation dereferenced a missing slot. It now ends as Pi's does, as the abandonment of the ended object (Pi's `#broken` returns the abandonment before a failure), and releases the keys it pinned. <!-- fix: 5b36301b3937a5e544bbcefa135c20f0f1659d6a -->
