### Fixed

- A pig that ended while an extension cell was still starting, or right after one had started, left an empty `pig-packed-*.log` file in the temporary directory: the exit killed only the processes it already knew of, and the removal of the log of a killed process ran in a goroutine the exit outran. The exit now ends every packed cell process, including one still starting, and removes its log before the pig exits; the log of a failure that was reported stays. <!-- fix: cc9cc2f3e -->
