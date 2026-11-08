### Changed

- Rendering a long transcript is faster, with the same output: word wrapping cuts a line into words without copying them (`WrapTextWithAnsi` on a long paragraph takes 26 µs instead of 102 µs and makes 26 allocations instead of 905), and the Markdown inline text rule classifies characters by table. The first fullscreen frame over a resumed session of 1,000 entries costs about 32% less CPU (333 ms to 227 ms); Pi-Bolt's `long_transcript` row (answer streaming into a 4,000-entry session, fullscreen) drops from 0.90 s to 0.48 s of median CPU.
