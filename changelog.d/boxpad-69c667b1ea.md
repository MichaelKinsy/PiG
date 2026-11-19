### Fixed

- Changing the output padding now applies at once to the branch-summary, compaction-summary and skill-invocation cards already in the transcript. Their padding change reached the card's box without telling the transcript, which kept showing the card at the old padding until the card changed for another reason. <!-- fix: 69c667b1ea19576a74b9d3a02c025a06843642ca -->
