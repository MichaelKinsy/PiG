### Changed

- The Durable core group-commits like Pi `e5c6182b`: a Session no longer holds its line while Storage stores a commit. Commits that arrive while one call is in flight go out together in the next call, one seq per part, and outcomes and publications are delivered in line order, one commit per macrotask. `Storage.commit` in the core's hosts takes batches and a receive callback; a Storage that has no `publishing` scope is called as before. Direct Storage commits keep one core session.

### Fixed

- Large nested-call fan-outs in the Durable core no longer re-send the whole document per call: the core sends the operations since the revision the host holds, and arrays that are only appended to are tracked as a tail. 1000 nested calls take 2.05 s instead of 4.94 s and 4000 calls 8.5 s instead of 49.3 s, with allocations per call flat. <!-- fix: 9883d4b247 -->
