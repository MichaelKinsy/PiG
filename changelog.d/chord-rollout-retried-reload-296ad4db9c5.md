### Fixed

- A Chord worker rollout no longer lists a retried failed worker twice or sends it a duplicate reload for the scripts it just started with, and no longer sends a duplicate reload to a starting worker waiting on a stale lock when a rollout ships the same script set (since v0.7.0). <!-- fix: 296ad4db9c5 --> <!-- fix: 1adf8da8a48 --> <!-- cause: Workers.Rollout released the component loop around c.route.Retry without skipping retried addresses in its reload loop, and startChild set spawned.shipped after options.Lock and ClearStaleLock instead of directly after Plugins.Scripts. -->
