### Changed

- A start that made no highlight lookup before the remaining syntax grammars loaded no longer repaints once they have: the cursor-only frame Pi writes there is skipped, and the screen is unchanged (D618). A start that looked a highlight name up before the load still repaints.
