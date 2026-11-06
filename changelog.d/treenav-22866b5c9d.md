### Fixed

- Session files with large tool outputs opened slowly when `/tree` navigated or the transcript repainted: a parallel read split its records by line count, so a session of few large records was decoded on one goroutine. The read splits by record bytes now; the cold context read of a 27 MB, 600-entry session went from 830 ms to 82 ms on 16 cores. <!-- fix: 22866b5c9dca8fa558814ab99d710590b20532da -->
