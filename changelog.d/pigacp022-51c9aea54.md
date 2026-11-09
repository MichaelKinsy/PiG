### Changed

- The `pig-acp` release archives are built from `@pi-in-go/pigpen-acp` 0.2.2, so `pig-acp --version` reports 0.2.2 and a relative `--pig` and the default `pig` resolve beside `pig-acp`, not against the session working directory or `PATH`; a registry entry that starts `./pig-acp` finds the `pig` of its archive. The archive smoke test now starts `pig-acp` with `--pig ./pig` and with no `--pig`, from an unrelated directory and a `PATH` without any pig.
