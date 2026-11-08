### Fixed

- A registry's value source (`host.Registry`, `self.Registry`) reads, watches and observes inside a section of the host's loop: the value composes when it is read and the source records its waiters as objects park, so a Go goroutine that read or watched it (a native's opener, a job; p2's worker start) raced them. Under `-race` that report killed a p2 worker with `the link closed`. <!-- fix: p2-base-e2e -->
