### Changed

- `pig --version` and `pig -v` now print Pi's own version, the package version of the pinned Pi build, exactly as `pi --version` does, so a launcher that checks Pi's version (T3 Code's Pi provider read `0.7.0+1.5.0` as Pi 0.7.0) accepts PiG. PiG's release is in `pig version`, which prints the PiG release and the Pi version in separate fields; the help and diagnostics banner and the interactive startup line still show `<PiG release>+<Pi release>`. Self-update, the installer and the release scripts read the release from `pig version`.

### Removed

- `PIG_VERSION_FORMAT` is gone: `pig --version` always prints Pi's version, so the opt-in has nothing left to switch.
