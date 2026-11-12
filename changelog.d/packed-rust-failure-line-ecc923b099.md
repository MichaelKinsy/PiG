### Fixed

- A packed Rust cell whose two members both failed to build could report the second member's panic as the first member's load error. The runner started the next member while the failed one was still writing its line, so the panic output of the next member split the line, and the host, finding no line of its own for the first member, read the last panic in the log. The next member now starts after the failed member's line is written and the host's wait for it has ended, as the Go runner already did. <!-- fix: ecc923b099d -->
