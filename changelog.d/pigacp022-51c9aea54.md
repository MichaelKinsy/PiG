### Changed

- The `pig-acp` release archives are built from `@pi-in-go/pigpen-acp` 0.2.2, so `pig-acp --version` reports 0.2.2 and a relative `--pig` such as `./pig` resolves against the directory of `pig-acp`, not the session working directory, and without `--pig` the `pig` beside `pig-acp` is used before any `pig` on `PATH`; a registry entry that starts `./pig-acp` finds the `pig` of its archive.
- The `pig-acp-<version>-<os>-<arch>` archives are flat: `pig-acp` and `pig` sit at the archive root, not under a versioned `pig-acp-<version>-<os>-<arch>/` directory, so the ACP registry entry's fixed `cmd` (`./pig-acp`, `./pig-acp.exe` on Windows) stays valid at every release. Extract them into a directory of your choosing.
