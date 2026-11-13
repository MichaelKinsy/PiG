### Fixed

- `chord/nextnode/plugins`: the copy of a Go core facet's keyed member a plugin reads (`Files.summary`) is applied in the order the Go host committed. The documents are the first frame the Go side sends, the commits made while they were read follow them (before, a commit that reached the Node host in the same chunk as the documents was applied first and then retired or overwritten by them), a slot that ends while it is read is read again, and a document change refused because the object ended is not reported as a fatal error of the Node host. <!-- fix: 7ddb7c1a27 -->
