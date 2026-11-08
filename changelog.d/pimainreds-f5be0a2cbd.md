### Changed

- A Markdown component keeps the wraps, inline renderings, highlighted code and resume point of a streamed render only through a weak pointer, as Pi's `markdown.ts` holds its rendered blocks in a `WeakRef`: a garbage collection releases them and the next render rebuilds them, with the same rows.
