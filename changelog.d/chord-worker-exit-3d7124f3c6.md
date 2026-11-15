### Fixed

- chord: a message sent to a worker that had just ended no longer discards what the worker wrote before it ended. The failed write closed the connection under its reader, so messages not yet told (a `fatal` report with the worker's reason) were dropped. As Node's `send`, a failed write now only tells its sender; the reader ends the channel after it told what the worker wrote. Present in v0.7.0. <!-- fix: 3d7124f3c6d4a1887ebb5aa65113b1b204e3f380 -->
