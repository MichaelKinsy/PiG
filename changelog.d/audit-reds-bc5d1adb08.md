### Fixed

- durable harness: closing a Harness on one goroutine while a commit was being published on another could start a scheduler pass after the close had waited for the scheduler's passes. The pass ran unjoined, and the close could panic with `sync: WaitGroup is reused before previous Wait has returned`. A pass now joins the scheduler when it is scheduled, so the close waits for it. <!-- fix: bc5d1adb08b7a87d64d5258a9f5df949d9374886 -->
