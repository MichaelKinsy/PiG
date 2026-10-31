### Fixed

- `pi update` no longer stops with `git clean -fdx failed with code 1` when a Git package has files that cannot be deleted, for example a native module another pi session still has open on Windows. pi now warns that files may be in use by another pi session, installs the dependencies anyway, and retries the clean on the next update. Carried forward from Pi e3da248e (earendil-works/pi#11648), past the 1.4.0+b3a29f11 pin. <!-- fix: e3da248e -->
