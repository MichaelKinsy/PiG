### Changed
- Long streamed answers use far less CPU and memory. Text, thinking and tool-call arguments are no longer copied again for every token, tool-call arguments are parsed from the bytes a token adds instead of from the start, and a published stream event copies only what changed since the previous one. On a stream of 50,000 text deltas and 20 tool calls the builder dropped from 3.4 s and 7.7 GB allocated to 0.15 s and 49 MB; the output is unchanged.
- The extension host reuses buffers for incoming frames of up to 1 MiB instead of allocating one per frame.
