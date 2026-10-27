### Fixed

- The Node SQLite driver keeps the 256 most recently used prepared statements per connection instead of every statement it ever prepared, closing the evicted ones, so callers that generate SQL of unbounded shapes no longer grow it without bound. <!-- fix: f8d96b7ffdf1f68f073d74128dffd9fedfa3a060 -->
