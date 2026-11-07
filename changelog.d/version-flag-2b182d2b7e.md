### Changed

- `pig --version` and `pig -v` now print Pi's own version, the package version of the pinned Pi build, exactly as `pi --version` does, so a launcher that checks Pi's version (T3 Code's Pi provider rejected the composite version) accepts PiG. PiG's release is in `pig version`, which prints the PiG release and the Pi version in separate fields; the help and diagnostics banner and the interactive startup line still show `<PiG release>+<Pi release>`. Self-update, the installer and the release scripts read the release from `pig version`.

### Removed

- `PIG_VERSION_FORMAT` is gone: `pig --version` always prints Pi's version, so the opt-in has nothing left to switch.

### Upgrading

- If you set an update command (`PIG_SELF_UPDATE_COMMAND`, `PI_SELF_UPDATE_COMMAND` or the `selfUpdateCommand` setting), updating from 0.7.0 to 0.7.1 prints one harmless warning, "Ran <command>, but `pig --version` reports 1.5.0, not 0.7.1". The running 0.7.0 still reads the version from `pig --version`; the update itself succeeded. From 0.7.1 on, `pig update` reads the release from `pig version`, and the warning does not recur.
