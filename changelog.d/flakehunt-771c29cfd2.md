### Fixed

- CI: `waitForRenderProxy`, the wait of the render proxy and packed padding tests for the extension's answer, is bounded by the hang budget `testbudget.Wait` instead of a fixed one second; `TestPackedMessageOutputPadAcrossSDKs/go` failed once with "render proxy condition was not met" on a loaded host (not reproduced since: 0 of 108 runs under 150 busy loops, before and after). <!-- fix: 771c29cfd2 -->
