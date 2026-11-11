### Fixed

- A durable `Submission.Wait` or task wait whose context was cancelled while earlier commits were still publishing returns the cancellation cause as the error of that call; a regression test with a slow `Storage.Publishing` (Pi `d9d535b6c9e4`, #11963, #11820) pins it. Pi needed a fix so Node reported no unhandled promise rejection; Go has none, so no Go source changed. <!-- fix: bc5690c60e65f27ebb9dce1dae4db93f6d427653 -->
