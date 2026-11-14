### Fixed

- A write to `models-store.json` that fails partway (a full disk, a size limit, the process killed) no longer leaves the file truncated and loses every provider's cached model catalog. The file is replaced through a temporary file in the same directory, and a failed write leaves the old content. <!-- fix: cdd5b98d3 --> <!-- cause: the store was written in place with os.WriteFile, which truncates the file before it writes, so any failure after that left a partial file -->
