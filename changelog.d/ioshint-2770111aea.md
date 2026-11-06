### Changed

- `install.sh` and the npm launcher now say that PiG doesn't run on iPhone, iPad, or iSH, and how to use PiG from such a device (run it on a computer you can reach and connect with `ssh you@your-computer pig`), instead of reporting an unsupported CPU architecture (`iPhone18,3`) or a missing npm package for `ios/arm64`. Both still exit with status 1.
