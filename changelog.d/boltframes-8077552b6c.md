### Fixed

- A `write` tool call that streams a large file no longer stalls one frame in twenty for 20-30 ms: the highlighting of the whole file that is kept warm while the content arrives runs in slices of about 4 ms per frame instead of 8-32 KB at a time. The lines the card draws are unchanged. <!-- fix: 8077552b6ce79ba4a53f57bffaa41315f2e41126 -->
