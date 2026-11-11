### Fixed

- A Chord activation that waited at the host data gate while an import or delete ran no longer crashes the host when its object ended meanwhile (a reload that no longer serves the type, a close). Its documents were gone, and the activation dereferenced a missing slot. It now fails as Pi's does, as a failed activation (or the abandonment that came first), and releases the keys it pinned.
