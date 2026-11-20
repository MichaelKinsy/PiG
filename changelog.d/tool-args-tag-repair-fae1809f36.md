### Fixed

- Malformed completed tool-call arguments whose `oldText`, `newText`, or `content` property separator lost `: "` into `>` (for example `"newText">...`) are now repaired after standard JSON parsing and string repair fail, while valid JSON, other properties, partial streams, and non-object roots keep their original result or error. <!-- fix: fae1809f36 -->
