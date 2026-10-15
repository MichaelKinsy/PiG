# Organise files

**Goal:** sort a messy folder (a Downloads folder, a pile of scans) into tidy subfolders, with a plan you approve first and a log that undoes every move.

**You need:** PiG signed in to a model ([Your first ten minutes](/docs/latest/first-run)) and a folder to tidy. This is the walkthrough that changes your files most, so it uses every habit from [Applied use cases](/docs/latest/applied-use-cases#five-habits-that-make-undo-possible): a copy, a plan first, a log and no deletion.

**Time:** ten minutes.

## Before you start

Work on a copy the first time. Copy the folder, then start PiG in the copy:

```bash
cp -R ~/Downloads ~/Downloads-copy
cd ~/Downloads-copy
pig
```

The screens below come from a folder with eight files: two invoices, a budget, a notes file, two photos, a screenshot and an installer.

## Step 1: ask for a plan, not for changes

```text
Look at the files in this folder. Do not move or rename anything yet. Propose folders by kind of file, and write the plan as a table to plan.md.
```

PiG lists the folder, writes `plan.md` and stops. The only change so far is the new file `plan.md`:

```text
 $ ls -1

 ... (3 earlier lines, ctrl+o to expand)
 budget.csv
 invoice-april.pdf
 invoice-march.pdf
 notes.txt
 setup.dmg

 Took 0.0s

 write plan.md

 # Plan

 | File | Goes to |
 |---|---|
 | invoice-march.pdf, invoice-april.pdf | Documents/Invoices |
 | budget.csv, notes.txt | Documents/Notes |
 | IMG_0412.jpg, IMG_0413.jpg, Screenshot 2026-09-03.png | Pictures |
 | setup.dmg | Installers |

 I did not move anything. plan.md lists where each of the 8 files would go: invoices, notes,
 pictures and one installer.
```

Open `plan.md`. If you disagree, say so now:

```text
Put the screenshot in a folder called Screenshots instead, and update plan.md.
```

Nothing has moved yet, so you can ask for as many changes to the plan as you like.

## Step 2: approve and record every move

```text
The plan is good. Do it. Use mv, never delete. Record every move in moves.log as a command that reverses it.
```

PiG creates the folders and moves the files with `mv`. It also writes one reversing command per move into `moves.log`. The model chooses the exact commands. This is the screen after one run, with the file list printed at the end:

```text
 ... (5 earlier lines, ctrl+o to expand)
 ./Pictures/IMG_0412.jpg
 ./Pictures/IMG_0413.jpg
 ./Pictures/Screenshot 2026-09-03.png
 ./moves.log
 ./plan.md

 Took 0.1s

 Done. All 8 files are in their new folders, and moves.log has one undo line per file.
```

Press `Ctrl+O` to show the hidden lines.

`moves.log` now holds eight lines, one per file. Each line moves a file back to where it was:

```text
mv "Documents/Invoices/invoice-march.pdf" "./"
mv "Documents/Invoices/invoice-april.pdf" "./"
mv "Documents/Notes/budget.csv" "./"
mv "Documents/Notes/notes.txt" "./"
mv "Pictures/IMG_0412.jpg" "./"
mv "Pictures/IMG_0413.jpg" "./"
mv "Pictures/Screenshot 2026-09-03.png" "./"
mv "Installers/setup.dmg" "./"
```

## Step 3: check the result

Compare the folder with the plan:

```bash
ls -R
```

Two checks catch most mistakes. Count the files before and after (the number in the folder you copied must equal the sum in the new folders), and open two files from each new folder.

## Undo

You can undo with PiG or without it. Without it is better, because it does not depend on the model.

1. In PiG, type a line that starts with `!`. PiG runs it as a shell command itself and shows the output. The model is not involved. A line that starts with `!!` runs it too but keeps the output out of the conversation (Pi `packages/coding-agent/src/modes/interactive/interactive-mode.ts:3347-3348`):

   ```text
   !sh moves.log
   ```

2. Or leave PiG and run it in the folder:

   ```bash
   sh moves.log
   ```

All eight files are back where they started. The empty folders PiG made stay behind. `rmdir` removes an empty folder and refuses to remove one with anything in it, so it is safe. Name the folders from your own `plan.md`, deepest first. `find . -mindepth 1 -type d` lists them. For the plan above the command is:

```bash
rmdir Documents/Invoices Documents/Notes Documents Pictures Installers
```

If your model chose other folder names, such as `Images` and `Data`, use those. `rmdir` prints `No such file or directory` for a name that does not exist and changes nothing.

Delete `plan.md` and `moves.log` when you are done. If you worked in a copy, you can instead delete the copy: `rm -r ~/Downloads-copy`.

## When the model skips the log

A model can forget a request. Check that `moves.log` exists and has one line per file **before** you rely on it. If it is missing, you have not lost anything: ask PiG to write the log from the plan (`Write moves.log from plan.md, as commands that move each file back`).

## Variations

- **By date:** "Group the photos into folders named by year and month, using each file's modified date."
- **Rename, not move:** "Rename the invoices to `YYYY-MM-DD-name.pdf`. Show me the old and new names in a table first and wait."
- **Find duplicates:** "List files with identical content. Do not delete any."
