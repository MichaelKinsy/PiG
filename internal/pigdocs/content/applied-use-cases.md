# Applied use cases

Use this page when someone who does not program asks PiG for everyday work: summarise documents, clean a spreadsheet, draft text, produce a report on a schedule or tidy files. Each task below gives the prompt to type, how to check the result and how to undo it. The full walkthroughs, with the screens PiG prints, are on the website: <https://pi-in-go.dev/docs/latest/applied-use-cases>.

## Rules to give the user first

- PiG acts without asking. The `write` tool overwrites a file at once, and a command runs at once. PiG shows what it did afterwards. It is not a sandbox (<https://pi-in-go.dev/docs/latest/security>; `trust.md` covers project trust).
- Everything PiG reads is sent to the model provider the user chose.
- PiG saves the conversation under `~/.pig/agent/sessions/`. `--no-session` keeps it in memory only.
- Work on a copy of the folder, ask for a plan before changes, ask for new files instead of edits, ask for a log of every change, and say "never delete".
- `Esc` stops the model and any running command. Changes already made stay made.
- `/tree` and `/fork` go back in the conversation. They never change files.
- The model's description of its work can be wrong. Open the file it wrote and compare it with the source.

## Tasks

**Summarise a folder of PDFs.** PiG's `read` tool cannot read PDFs: it returns raw bytes. The model converts them with `pdftotext` (Poppler: `brew install poppler`, `sudo apt install poppler-utils`). A scanned PDF has no text to convert. Prompt: `Read every PDF in this folder. Write summaries.md with one short paragraph per file, then a three-bullet overall summary. Do not change the PDFs.` Check: open `summaries.md`, compare two figures with the PDFs. Undo: delete `summaries.md`.

**Clean a spreadsheet.** Export to CSV first (`.xlsx` is binary). Prompt: `Open sales.csv. Make a cleaned copy called sales-clean.csv: one date format (YYYY-MM-DD), trimmed spaces, consistent capitalisation, no blank or duplicate rows, amounts as plain numbers. Never change sales.csv. List every change in changes.md.` Check: `wc -l` both files, open the original, ask which dates were ambiguous. Undo: delete the new files (`ls -lt` lists the newest).

**Draft and edit a document.** Prompt: `Read notes.txt and write a one-page status update for my manager into update.md. Plain language, three short paragraphs.` Then: `Before you change update.md again, copy it to update.v1.md. Then make the first paragraph friendlier. Change nothing else.` Check: `diff update.v1.md update.md`. Undo: `cp update.v1.md update.md`. Typing `@name` in the editor completes a file name only. The model opens the file itself.

**Automate a weekly report.** `pig -p` runs one prompt and exits. Script: `pig -p --no-session --tools read,write @this-week.csv "<instruction>" >> weekly-report.log 2>&1`, run from cron with the full path to `pig`, after `pig login` (cron has no shell profile, so an exported key is not there). A missing `@` file exits 1 with `Error: File not found`. An empty one is skipped silently, so guard with `[ -s this-week.csv ] || exit 1`. Undo: remove the cron line and the script.

**Organise files.** Prompt 1: `Look at the files in this folder. Do not move or rename anything yet. Propose folders by kind of file, and write the plan as a table to plan.md.` Prompt 2: `The plan is good. Do it. Use mv, never delete. Record every move in moves.log as a command that reverses it.` Undo: `!sh moves.log` in PiG, or `sh moves.log` in a shell. `rmdir` removes the empty folders that remain.

## If the user feels lost

Four things are enough: type what you want in plain words, press `Esc` to stop, type `/` to see commands, and ask PiG about PiG. For install, sign-in and the first task, read `onboarding.md` and the website page <https://pi-in-go.dev/docs/latest/first-run>.
