### Fixed

- The alternate screen's viewport accessors (`ViewportTop`, `IsFollowingOutput`, `ScrollBy`, `ScrollToTop`, `ScrollToBottom`) read the layout of the last frame under the renderer's lock. An extension's `tuiAlt` host call reads them from its own goroutine while a frame stores a new layout, and the race detector reported the Python SDK's conformance row. <!-- fix: 7db4e68fc4 -->
