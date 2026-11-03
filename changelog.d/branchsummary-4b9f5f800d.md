### Fixed

- `/tree` with summarization no longer sends an attempt that was hidden from the model back to the summarizer. When a partial assistant reply failed and automatic retry succeeded, the branch summary still contained the failed attempt, and a context edit that replaced an entry's content was ignored. The summary input now applies each entry's latest context edit through the projection the live context and compaction use: an omission drops the entry and a replacement swaps its content (Pi d0fe21308). <!-- fix: 4b9f5f800d -->
