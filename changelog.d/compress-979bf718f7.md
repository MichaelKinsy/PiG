### Security

- Updated `github.com/klauspost/compress` from v1.19.2 to v1.20.3, which fixes GO-2026-6865 (a panic on two-block zstd frames). PiG reached it through `zstd.Encoder.EncodeAll`, which compresses the request body for the Codex provider.
