### Changed

- Streaming frames are drawn on time. While a reply or a tool call streams in, PiG draws its frames 15 to 16 ms apart, as Pi does (Node cuts a timer delay to whole milliseconds), and on Linux it keeps the deadline of the next frame on a kernel timer, because the Go runtime timer fired up to a millisecond late there. The median time between frames falls from 16.7 ms to 15.6 ms, and the same stream is drawn in 5 to 7 % more frames.
