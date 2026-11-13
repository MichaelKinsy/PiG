### Fixed

- chord: a message sent to a worker that had just ended no longer discards what the worker wrote before it ended. The failed write closed the connection under its reader, so messages not yet told (a `fatal` report with the worker's reason) were dropped. As Node's `send`, a failed write now only tells its sender; the reader ends the channel after it told what the worker wrote. <!-- fix: 49534d19484ce2fdd3fa53c284ae55754522dfa8 -->
