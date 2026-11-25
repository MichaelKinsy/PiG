### Fixed

- A live `kit.Loader` returned from an `EntryViewRenderer`, `MessageViewRenderer`, or `ToolViewRenderer` now animates inside a caching transcript container instead of staying frozen on its first frame (#567). Present since v0.7.0. <!-- fix: efb2e5690f -->
