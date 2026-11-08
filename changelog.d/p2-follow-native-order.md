### Fixed

- A p2 state source following a native (the provider login's state, the models and environments snapshots) always ends on the native's state after its last change when several goroutines change it at once: a goroutine's stale read no longer lands after a newer one, which left a cancelled login showing "working" for good. <!-- fix: p2-follow-native-order -->
