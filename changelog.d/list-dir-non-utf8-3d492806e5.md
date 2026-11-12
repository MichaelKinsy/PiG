### Fixed

- `listDir` of the local execution environment leaves out an entry whose name is not valid UTF-8, as Pi does (Node decodes the name with replacement characters, so its `lstat` finds nothing and #12055 skips the entry); the remote environment already matched. <!-- fix: 3d492806e5 -->
