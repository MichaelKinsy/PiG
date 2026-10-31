### Fixed

- A streamed OpenRouter-style `reasoning_details` no longer costs more with every delta. Each delta merged into the entry list and then wrote the whole thinking signature again, so a reasoning stream of 10000 deltas took seconds and gigabytes (10000 discrete `reasoning.encrypted` entries: 94 s and 51 GB; 10000 merged text deltas: 5.5 s and 6.4 GB). The details now accumulate in memory and the signature is written once, when the thinking block ends or the stream fails, as in Pi; 10000 merged text deltas take 87 ms and 25 MB, and the signature is unchanged. <!-- fix: 7aab6c26e5 -->
