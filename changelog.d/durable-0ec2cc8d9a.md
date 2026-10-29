### Fixed

- A durable session no longer has a data race between a call to `DeferUntilPublished` from another goroutine (a harness's wait for idleness makes one) and the commit that arms its publication: the commit read the length of the publication's queue after it released the lock that the call appends to it under, so the race detector could report the two in a long durable/harness run. The commit now checks the queue under the lock. <!-- fix: 0ec2cc8d9a, d6110092aa -->
