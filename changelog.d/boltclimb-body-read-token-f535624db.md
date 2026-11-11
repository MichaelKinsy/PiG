### Changed

- A streamed response costs less CPU in a short session. Every read of the response body that waited for the network asked for the calling goroutine's ID, which formats the goroutine's stack header (25 microseconds) until 17 distinct goroutines have asked, so the first requests of a session paid it on each chunk: 15% of the CPU profile of a 400 KB write call's response. The read now compares the goroutine's runtime record address (1.8 nanoseconds, no proof needed) with the one it recorded.
