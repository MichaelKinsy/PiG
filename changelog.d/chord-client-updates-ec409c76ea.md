### Fixed

- A chord client's watch callback returned at once and let a newer script set be delivered while an update ran, which left a stale retry armed and doubled the next retry pause; the callback now waits for its update, as Pi awaits it, so the newer set is delivered after the update ended. <!-- fix: ec409c76ea -->
