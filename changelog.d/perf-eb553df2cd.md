### Changed

- Streaming a model response costs less CPU and memory per token: a text delta from Anthropic, OpenAI Chat Completions and OpenAI Responses now takes 5 to 6 allocations instead of 12 to 19, a tool-call delta 20 to 25 instead of 42 to 44, and a Google delta 28 instead of 69. Bytes per delta fall on every provider shape, and none costs more time, bytes or allocations than before. What a consumer of the stream reads does not change.
