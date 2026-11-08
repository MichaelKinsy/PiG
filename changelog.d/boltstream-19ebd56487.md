### Changed

- Rendering a long transcript is faster, with the same output byte for byte: word wrapping cuts a line into words without copying them (`WrapTextWithAnsi` on a long paragraph takes 35 µs instead of 100 µs and makes 26 allocations instead of 905), and the Markdown inline text rule classifies characters by table. The first fullscreen frame over a resumed session of 4,000 entries costs about 30% less CPU; Pi-Bolt's `long_transcript` row (answer streaming into a 4,000-entry session) drops from about 1.4 s to about 1.0 s of CPU.
