### Changed

- `install.sh` and the npm launcher now say that PiG doesn't run on iPhone, iPad, or iSH, and how to use PiG from such a device (connect with `ssh` to a computer that has PiG installed, then run `pig` there), instead of reporting an unsupported CPU architecture (`iPhone18,3`) or a missing npm package for `ios/arm64`. Both still exit with status 1.
