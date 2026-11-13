### Changed

- The release binary is 24,576 B smaller on linux/amd64 and 25,088 B smaller on windows/amd64: the plugin UI registry sorts its mounts and the members of a canonical JSON object with the shared sort of `internal/sortx`, so two copies of the sort that came with the registry are no longer linked.
