### Fixed

- Changing the output padding now applies at once to the branch-summary, compaction-summary and skill-invocation cards already in the transcript. Their padding change reached the card's box without telling the transcript, which kept showing the card at the old padding until the card changed for another reason. <!-- fix: ebaa4b5e2da687d9a96c5d01e5d3871c90d8ce48, f2641e89066325f41b2ca323cd3a07d615b9d399 -->
