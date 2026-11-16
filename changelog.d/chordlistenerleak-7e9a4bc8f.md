### Fixed

- Chord: an abort listener that ran leaves every source it listened to, and an emptied listener set is dropped. Before, a context that outlived its waits (a link's or a host's) kept every wait that had ended by an abort, and the memory of its busiest moment. <!-- fix: 7e9a4bc8f5e5bfccd4b1cb59e296082932935e06 -->
