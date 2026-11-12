### Fixed

- The find tool no longer returns no results for patterns that start with `./`, such as `./src/*.ts`; the leading `./` is dropped, as under Pi (upstream #12020). <!-- fix: 2b29409f01b0b6937a4572c237164e474ed30bd3 -->
