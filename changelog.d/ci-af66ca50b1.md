### Fixed

- The CI image version-literal test no longer reports the Pi part of an image tag (`1.4.0.b3a29f11`, written by `make pin-sync` and checked by `make pin-check`) as a hard-coded version when the pinned commit starts with a letter. <!-- fix: af66ca50b1a62039c2038d15492e809eacfa6e1c -->
