### Changed

- A session without a file (`--no-session`, an in-memory session) no longer encodes a built-in tool's result details only to learn that they can be encoded. For the details of `read`, `bash`, `grep`, `find` and `ls` that is decided from their type, once; details of any other type are encoded as before. What the session holds is unchanged, and a session with a file writes the same lines. In the 40-prompt benchmark session this was 43 KB allocated per file read.
