### Changed

- A model request that cache warming skips (warming off, a request that cannot be replayed, a model without a cache lifetime) no longer copies the session's message list: 64 bytes per message once per request, 0.8 % of the bytes a 20-prompt session allocates.
