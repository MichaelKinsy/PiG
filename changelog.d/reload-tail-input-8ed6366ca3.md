### Fixed

- A key received after a /reload had completed could be dropped by the input loop; the editor now receives it, in the order typed. Keys typed while the reload runs are still dropped, as in Pi. <!-- fix: 8ed6366ca3b2468bef84bac9a6a9047d50f29341 -->
