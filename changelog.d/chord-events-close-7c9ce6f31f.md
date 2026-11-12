### Fixed

- Chord: a contract with an event member that crosses between a Go host and a Node host as data is rebuilt with its event member; it was rebuilt as a method and refused with a fingerprint mismatch. Until events are delivered across the plugins bridge, `NodeHost.Get`, `NodeHost.Hold` and a bridged Go core facet fail for a type with an event member with an error that says events do not cross the plugins bridge. <!-- fix: 7c9ce6f31f -->
