### Changed

- The `pig` binary is 1.4 MB smaller. The Go, Rust and Python SDK sources that it embeds to stage for extension builds are stored gzipped when a file is 32 KiB or more (23 files, as the Rust and Python unicode tables already were); staging writes the same files back. `BundledFiles()` of the three bundles names such a file `<name>.gz`; `embed-bundle -gzip-over` writes the compressed copy beside the file.
