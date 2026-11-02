### Fixed

- `tool_search` and codemode `searchTools()` rank a tool that matches more of the query's distinct terms, in any field (name, description and schema, namespace), above one that matches fewer, whatever their scores. The name weighting of the previous ranking change put `peekaboo__window` above `peekaboo__image` for `screenshot window` and `peekaboo__app` above `peekaboo__see` for `describe app`. A query that is exactly a tool's name still ranks that tool first. Carried forward from Pi e033f255 (earendil-works/pi#11331), past the 1.4.0+b3a29f11 pin. <!-- fix: e033f255 -->
