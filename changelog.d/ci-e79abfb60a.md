### Fixed

- The CI image version-literal test no longer reports the Pi part of an image tag (`1.4.0.b3a29f11`, written by `make pin-sync` and checked by `make pin-check`) as a hard-coded version when the pinned commit starts with a letter. <!-- fix: e79abfb60aa208dbf774ad0f0baf7b14bbf26a52 -->
