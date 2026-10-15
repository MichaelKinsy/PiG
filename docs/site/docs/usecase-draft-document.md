# Draft and edit a document

**Goal:** write a first draft from your own notes, then change one part of it without touching the rest, with the earlier version kept so you can go back.

**You need:** PiG signed in to a model ([Your first ten minutes](/docs/latest/first-run)) and your notes in a plain text or Markdown file. PiG cannot read Word or Pages files. Save them as plain text first, or paste the text into a `.txt` file.

**Time:** ten minutes.

## Before you start

Put your notes in a new folder and start PiG there:

```bash
mkdir ~/status-update && cd ~/status-update
cat > notes.txt <<'EOF'
Week 12 notes
- API migration finished, 2 days late
- Hired one designer (starts Monday)
- Budget: 8% under plan
- Risk: vendor contract renewal due 30 Oct
EOF
pig
```

## Step 1: draft from the notes

```text
Read notes.txt and write a one-page status update for my manager into update.md. Plain language, three short paragraphs.
```

Name the file, the reader, the length and the tone. PiG shows the file it writes, then the model's message:

```text
 write update.md

 # Project update, week 12

 The API migration finished two days late. The team is now working on the remaining clean-up.

 A designer joins on Monday. Hiring is complete for this quarter.

 The project is 8% under budget. The one open risk is the vendor contract, which renews on 30
 October.

 I wrote update.md from notes.txt: three short paragraphs (progress, people, budget and risk).
```

Your model's draft will read differently. `write` creates the file, or overwrites it if it exists (Pi `packages/coding-agent/src/core/tools/write.ts:78-82`). The first draft is safe because `update.md` did not exist. A second `write update.md` would replace it.

### Naming a file in the editor

Typing `@` in the editor shows file suggestions. `Tab` completes the name. PiG sends your text as you typed it, including the `@name`. It does not paste the file's contents into your message. The model opens the file itself with its `read` tool. If you want the contents sent with your first message, start PiG with the file on the command line: `pig @notes.txt "Write a status update from this"`.

## Step 2: keep a copy, then change one thing

Before the second change, keep the first draft. A copy is the most reliable way back.

```text
Before you change update.md again, copy it to update.v1.md. Then make the first paragraph friendlier. Change nothing else.
```

PiG copies the file, then edits it. An edit is a replacement of one passage by another, and PiG shows it as a diff. Lines that start with `-` were removed, lines that start with `+` were added, and the numbers are line numbers in the file:

```text
 $ cp update.md update.v1.md && ls

 notes.txt
 update.md
 update.v1.md

 Took 0.0s

 edit update.md

  1 # Project update, week 12
  2
 -3 The API migration finished two days late. The team is now working on the remaining clean-up .
 +3 The API migration is done, two days later than planned. The team is tidying up the last
 details.
  4
  5 A designer joins on Monday. Hiring is complete for this quarter.
  6
  7 The project is 8% under budget. The one open risk is the vendor contract, which renews on 30
 October.

 I changed only the first paragraph. The other two are as they were.
```

The diff is the check. Only line 3 changed, and lines 1, 2 and 4 to 7 are context. In colour, PiG highlights the changed words inside the line (Pi `packages/coding-agent/src/modes/interactive/components/diff.ts:22-45`). Without colour the only sign is a stray space before the full stop, as in the diff above. Pi shows the same.

Repeat step 2 as often as you like, with `update.v2.md`, `update.v3.md` and so on.

## Step 3: check the result

1. Open `update.md` and read it as your manager would.
2. Compare it with the earlier version:

   ```bash
   diff update.v1.md update.md
   ```

   ```text
   3c3
   < The API migration finished two days late. The team is now working on the remaining clean-up.
   ---
   > The API migration is done, two days later than planned. The team is tidying up the last details.
   ```

   `3c3` means line 3 changed. `<` is the old text and `>` is the new text. No other line is listed, so no other line changed.
3. Check the facts against `notes.txt`: dates, figures and names. A model can invent a detail that sounds right.

## Step 4: copy the text out

Type `/copy` in PiG to copy the last message to your clipboard. Or open `update.md` and copy from there.

## Undo

- **The last edit:** `cp update.v1.md update.md` puts the first draft back.
- **The whole thing:** delete `update.md` and `update.v1.md`. `notes.txt` was only read.
- **With PiG:** type `Put update.md back to how it was in update.v1.md.` If you made no copy, type `Undo your last edit to update.md.` The model can see the diff it made in the conversation and reverse it. That works only while the edit is still in the conversation.

## Variations

- **Tone:** "Rewrite update.md for a customer instead of my manager. Write it to update-customer.md."
- **Shorter:** "Cut update.md to 100 words. Write the result to update-short.md and keep the original."
- **Proofread, do not rewrite:** "List spelling and grammar mistakes in update.md as a numbered list. Do not edit the file."
- **Your voice:** paste two paragraphs you wrote earlier and say "Match this style".
