### Fixed

- `pig update` now warns when an npm package update leaves the installed version unchanged: shortly after a publish `npm view` can report the new version while `npm install` still resolves the old one, and `Updated packages` was printed with nothing updated. The warning names the package, the installed and the registry version, and is printed in yellow. Carried forward from Pi 0d23d534 (earendil-works/pi#11536), past the 1.4.0+b3a29f11 pin. <!-- fix: 0d23d534 -->
