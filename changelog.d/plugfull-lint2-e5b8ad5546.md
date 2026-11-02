### Fixed

- `chord/nextnode/plugins`: a build error's column is esbuild's own (in bytes), as Pi prints it, where it was counted in UTF-16 units after non-ASCII text. <!-- fix: e5b8ad5546 -->
- `chord/nextnode/plugins`: a `NodeMember.Value` read that is waiting when the Node process dies returns the process's exit alone, not also a cancellation nobody asked for. <!-- fix: e5b8ad5546 -->
