### Fixed

- Starting `pig` from a working directory that has been deleted now prints "Error: The current working directory no longer exists. Change to an existing directory and try again." and exits 1, where it printed `get cwd:` with the operating system error. Carried forward from Pi 88ffcdc7 (earendil-works/pi#11509), past the 1.4.0+b3a29f11 pin. <!-- fix: 88ffcdc7 -->
