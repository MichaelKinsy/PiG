### Changed

- The plan-mode example tracks progress more tolerantly, as Pi's does: `[DONE: 1]`, `[DONE 1]`, `Done: 1` and "Step 1 done / is complete / finished" mark a step (never "Step 9 is not done"), thinking blocks count, only the last `Plan:` section is read, step text is no longer cut at 50 characters (the widget shortens it for display), steps that start with a backtick are kept, the widget refreshes every turn, "All steps complete" finishes the plan, and the plan-complete card waits for the next prompt instead of starting a turn.
