# Clean a spreadsheet

**Goal:** turn a messy CSV export into a clean copy: one date format, trimmed text, consistent capital letters, no blank or duplicate rows and plain numbers. The original stays as it is, and you get a list of every change.

**You need:** PiG signed in to a model ([Your first ten minutes](/docs/latest/first-run)) and your data saved as a CSV file.

**Time:** five to ten minutes.

## Why CSV

PiG reads text files. A spreadsheet saved as `.xlsx` or `.numbers` is not text: it is a packed file that only a spreadsheet program can open. PiG's `read` tool reads text files and images only (Pi `packages/coding-agent/src/core/tools/read.ts:96`: "Supports text files and images"). Export to CSV first:

| Program | Menu |
|---|---|
| Excel | File, Save As, "CSV UTF-8 (Comma delimited)" |
| Google Sheets | File, Download, "Comma Separated Values (.csv)" |
| Numbers | File, Export To, CSV |
| LibreOffice Calc | File, Save As, "Text CSV" |

A CSV file keeps one sheet, the values and none of the formulas or colours. Keep your workbook for those.

## Before you start

Put a copy of the CSV in a new folder and start PiG there:

```bash
mkdir ~/sheet-clean && cp ~/Documents/sales.csv ~/sheet-clean/
cd ~/sheet-clean
pig
```

The screens below use this small file, `sales.csv`. It has inconsistent dates, stray spaces, mixed capital letters, a blank row, a repeated row and amounts written as `"1,300.00"` and `  780.25`:

```text
Date,Customer,Region,Amount
2026-03-01,Acme Ltd ,emea,1200.50
03/02/2026,acme ltd,EMEA,"1,300.00"
2026-03-02,Globex,APAC,950
2026-03-02,Globex,APAC,950
,,,
2026-03-05,Initech,amer,  780.25
5 Mar 2026,INITECH,AMER,410
```

## Step 1: ask for a cleaned copy and a change list

```text
Open sales.csv. Make a cleaned copy called sales-clean.csv: one date format (YYYY-MM-DD), trimmed spaces, consistent capitalisation, no blank or duplicate rows, amounts as plain numbers. Never change sales.csv. List every change in changes.md.
```

The prompt names the output files, says what must stay untouched and asks for a log. The model reads the file, writes a small script that does the cleaning and runs it. For a file this size, the script is one the model writes for itself. PiG shows it as a `write` step and folds long text to its first lines:

```text
 write clean.py

 import csv, datetime, re
 rows = list(csv.DictReader(open("sales.csv", newline="")))
 out, seen, log = [], set(), []
 for n, r in enumerate(rows, start=2):
     if not any(v.strip() for v in r.values()):
         log.append(f"row {n}: blank row dropped"); continue
     d = r["Date"].strip()
     for f in ("%Y-%m-%d", "%m/%d/%Y", "%d %b %Y"):
         try: d = datetime.datetime.strptime(d, f).strftime("%Y-%m-%d"); break
         except ValueError: pass
 ... (10 more lines, 20 total, ctrl+o to expand)
```

You do not need to read the script. If you want to, press `Ctrl+O` to show all of it. Then PiG runs it. PiG folds long command output to its last lines:

```text
 $ python3 clean.py && cat sales-clean.csv && cat changes.md

 ... (6 earlier lines, ctrl+o to expand)
 2026-03-05,Initech,AMER,410.00
 # Changes

 - row 5: duplicate of an earlier row dropped
 - row 6: blank row dropped

 Took 0.0s
```

Press `Ctrl+O` to see the hidden lines, or open `sales-clean.csv`. It holds this:

```text
Date,Customer,Region,Amount
2026-03-01,Acme Ltd,EMEA,1200.50
2026-03-02,Acme Ltd,EMEA,1300.00
2026-03-02,Globex,APAC,950.00
2026-03-05,Initech,AMER,780.25
2026-03-05,Initech,AMER,410.00
```

This machine had Python installed. If yours has none, the model reports that and suggests another way, such as a command-line tool. It may also offer to install something. Say no to an install you did not expect, and ask for an approach that needs nothing new: `Do it with awk or with built-in tools only.`

## Step 2: check the work yourself

The model's closing message is a description. The files are the result. Check three things:

1. **The count.** Seven data rows went in (one was blank, one a repeat), so five rows come out. Compare:

   ```bash
   wc -l sales.csv sales-clean.csv
   ```

   ```text
     8 sales.csv
     6 sales-clean.csv
    14 total
   ```

   `wc -l` counts the header line too: 8 lines is 7 data rows, and 6 lines is 5.
2. **The original is unchanged.** Open `sales.csv`. It must still hold the messy text.
3. **The judgement calls.** `03/02/2026` can mean 2 March or 3 February. The model had to pick one. Ask:

   ```text
   Which dates in sales.csv were ambiguous, and how did you read each one?
   ```

   If it guessed wrong, say so: `My dates are day first. Redo sales-clean.csv.` The model rewrites `sales-clean.csv`. It is a new file, so `sales.csv` is still safe.

## Step 3: use the result

Open `sales-clean.csv` in your spreadsheet program. Keep `changes.md` with it. It is the audit trail that shows what was dropped and why.

## Undo

PiG added `sales-clean.csv` and `changes.md`, and in the run above also the script `clean.py`. Your model may name its script differently or run the cleaning without one. `ls -lt` lists the newest files first. Delete the files PiG added:

```bash
rm sales-clean.csv changes.md clean.py
```

If `ls` does not show one of these names, leave it out. Otherwise `rm` reports `No such file or directory` for that name and still deletes the others.

`sales.csv` was never written to. If you asked for an edit in place and regret it, restore your copy from `~/Documents`.

## Variations

- **Add a column:** "Add a Quarter column (Q1 to Q4) to sales-clean.csv, from the date."
- **Totals:** "Make a second file, totals.csv, with the total Amount per Customer, largest first."
- **Find problems without fixing them:** "List rows where Amount is zero, negative or missing. Do not change any file."
- **Check yourself:** "Recount the rows in sales-clean.csv from the file itself and tell me the number."
