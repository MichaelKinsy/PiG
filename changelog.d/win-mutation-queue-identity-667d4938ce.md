### Fixed

- On Windows, an edit or write waits for an earlier edit or write of the same file reached through a hard link even when the queued file was replaced meanwhile (an atomic write): the queue keeps the identity of the file as it was when the mutation queued, not the one the path names when a later mutation is compared. <!-- fix: 667d4938ce -->
