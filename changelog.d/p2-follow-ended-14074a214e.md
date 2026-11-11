### Fixed

- p2: a followed reference no longer misses the ended state it finishes with when its watch ends before its listener starts or terminates, and a follower's callbacks no longer run concurrently (a view room could stay open after its viewer's grant was revoked). <!-- fix: 14074a214eb4cbb297d7e68e8eae167323f36c7d -->
