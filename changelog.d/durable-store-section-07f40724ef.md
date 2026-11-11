### Fixed

- A Durable session on a Chord store no longer races the host's loop when a commit is published: the publication of each commit now runs as a section of the host's loop instead of on the writer's goroutine, which touched the host's batch while the loop ran its own commits (a data race, seen under `-race` when a p2 server reloads a plugin while a worker streams). A caller that blocks on a durable commit from inside a host section now waits for itself; commit from a goroutine of its own. <!-- fix: 07f40724ef -->
