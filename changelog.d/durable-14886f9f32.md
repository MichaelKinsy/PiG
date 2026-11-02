### Fixed

- A call to `Session.DeferUntilPublished` from a goroutine other than the publishing one (a Harness's wait for idleness makes it from its caller's) no longer races the commit that arms the publication it lands on: the commit read the publication's queue without the lock the call appends under, which the race detector reported. <!-- fix: 14886f9f32 -->
