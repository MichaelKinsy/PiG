### Fixed

- The find tool no longer returns no results for patterns that start with `./`, such as `./src/*.ts`; the leading `./` is dropped, as under Pi (upstream #12020). <!-- fix: ac7ebbf1a5 -->
