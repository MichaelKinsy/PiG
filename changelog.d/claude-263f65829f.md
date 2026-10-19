### Changed

- `durable/env`: `ListFiles` is a method of `FileSystem` (and so of `ExecutionEnv`), as Pi's optional `listFiles?` is, and the separate `FileLister` interface is gone. An environment without a lister, such as `node.NodeExecutionEnv`, fails `ListFiles` with a `not_supported` FileError, where Pi leaves the method undefined.
