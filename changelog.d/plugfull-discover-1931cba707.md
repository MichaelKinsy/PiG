### Added

- `chord/nextnode/plugins`: the Node subprocess that evaluates plugins is found beyond `PATH`: `PIG_NODE` (an executable; when set it is the only candidate), then `node` on `PATH`, then the usual locations of nvm, fnm, Volta, asdf, mise, `/usr/local/bin`, `/opt/homebrew/bin` and `/usr/bin` (on Windows `%NVM_SYMLINK%`, the versions of nvm and fnm, Volta, `%ProgramFiles%\nodejs` and `%LOCALAPPDATA%\Programs\nodejs`). The first candidate that is Node 22.19.0 or newer wins; when none is, the error names the newest version found or where it looked (D221).

### Changed

- `chord/nextnode/plugins`: a plugin input the build cannot read is reported at the import that names it (`worker.ts:1:19: EACCES: permission denied, open '<path>'`), where Pi prints the line of esbuild's own library; the difference is numbered D222.
