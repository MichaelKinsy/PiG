# Summarise a folder of PDFs

**Goal:** read every PDF in a folder and get one short summary per file plus an overall summary, written to `summaries.md`. Your PDFs are not changed.

**You need:** PiG signed in to a model ([Your first ten minutes](/docs/latest/first-run)), a folder of PDFs that contain real text (not scans), and a tool that turns a PDF into text. Step 1 checks for that tool.

**Time:** about five minutes.

## Why a PDF needs a converter

PiG's `read` tool reads text files and images (jpg, png, gif, webp, bmp). It does not read PDFs. Given a PDF it returns the raw bytes, which are unreadable to you and to the model (Pi's tool description says "Supports text files and images", `packages/coding-agent/src/core/tools/read.ts:96`). The model can run a program that converts the PDF to text, and PiG's `bash` tool runs it. The usual program is `pdftotext`, part of the Poppler package.

A scanned PDF holds pictures of pages, not text. `pdftotext` returns nothing for it. Use an OCR tool, or a model that accepts images, for those.

## Before you start

Copy the PDFs into a new folder and start PiG there. The walkthrough adds one file and changes none, but the habit costs nothing:

```bash
mkdir ~/pdf-summary && cp ~/Documents/reports/*.pdf ~/pdf-summary/
cd ~/pdf-summary
pig
```

## Step 1: check that PiG can read PDFs

```text
Can you read PDF files on this computer? Check what is installed. Do not change anything.
```

PiG runs a command to look for a converter. On a computer without Poppler it shows:

```text
 Can you read PDF files on this computer? Check what is installed. Do not change anything.

 $ ls *.pdf; command -v pdftotext || echo 'pdftotext: not found'

 customer-survey.pdf
 q1-sales-report.pdf
 q2-sales-report.pdf
 pdftotext: not found

 Took 0.0s

 There are 3 PDFs. This computer has no PDF-to-text tool installed, so I cannot read them yet.
 Installing poppler (brew install poppler, or sudo apt install poppler-utils) gives me pdftotext. I
 have changed nothing.
```

Each line that starts with `$` is a command PiG ran. `Took 0.0s` is how long it ran. The text under it is the model's answer.

If the answer names a path such as `/usr/bin/pdftotext`, skip to step 2.

The model may offer to install a converter for you ("Would you like me to install pdfplumber?"). Say no. Install it yourself with the table below, so you know what was added to your computer.

### Install the converter

Close PiG with `Ctrl+D`, install Poppler and start PiG again in the same folder:

| System | Command |
|---|---|
| macOS (Homebrew) | `brew install poppler` |
| Debian and Ubuntu | `sudo apt install poppler-utils` |
| Fedora | `sudo dnf install poppler-utils` |

On other systems, install any program that converts PDF to text and tell PiG its name. If you cannot install software, open each PDF in your PDF viewer, save it as text into the same folder and ask PiG to summarise the `.txt` files. That needs no converter.

## Step 2: summarise

```text
Read every PDF in this folder. Write summaries.md with one short paragraph per file, then a three-bullet overall summary. Do not change the PDFs.
```

PiG runs the converter once per file, then writes the summary. PiG folds long command output to its last lines. The line `... (4 earlier lines, ctrl+o to expand)` says some lines are hidden, and `Ctrl+O` shows or hides all tool output (`internal/codingagent/tool_render_shell.go:206`; Pi `packages/coding-agent/src/core/tools/renderers/bash.ts:78`):

```text
 $ for f in *.pdf; do echo "=== $f"; pdftotext -layout "$f" -; done

 ... (4 earlier lines, ctrl+o to expand)
 Q1 Sales Report
 Revenue grew 12% to 4.2M. The EMEA region led growth. Churn rose to 3.1%.
 === q2-sales-report.pdf
 Q2 Sales Report
 Revenue grew 8% to 4.5M. APAC launched. Churn fell to 2.7%.

 Took 0.7s

 write summaries.md

 # Summaries

 ## customer-survey.pdf
 The 2026 customer survey gives a net promoter score of 41. Customers' top complaint is slow
 onboarding; their top praise is support.

 ## q1-sales-report.pdf
 Q1 revenue grew 12% to 4.2M, led by EMEA. Churn rose to 3.1%.

 ## q2-sales-report.pdf
 Q2 revenue grew 8% to 4.5M, helped by the APAC launch. Churn fell to 2.7%.
 ... (5 more lines, 15 total, ctrl+o to expand)

 I read all 3 PDFs and wrote summaries.md: one paragraph per file and a three-bullet overall
 summary. The PDFs are unchanged.
```

The text your model writes will differ from this. The structure, a `write summaries.md` step after the conversion, is what to look for.

## Step 3: check the result

1. Open `summaries.md` in any text editor.
2. Open one PDF and compare two or three numbers with the summary. Models misread tables and round figures.
3. Look at the folder:

   ```bash
   ls
   ```

   ```text
   customer-survey.pdf
   q1-sales-report.pdf
   q2-sales-report.pdf
   summaries.md
   ```

   The only new file is `summaries.md`.

If a summary is wrong, tell PiG: `The Q2 summary says revenue grew 8%. Check the Q2 PDF again and quote the sentence you used.`

## Step 4: ask a follow-up

PiG keeps the conversation, so you can ask more without repeating the setup:

```text
Which of these documents mention churn? List each one with the figure it gives.
```

For a long PDF, ask for one section at a time. A model has a limit on how much text it can hold at once, and the footer at the bottom of the screen shows how much of it your conversation has used (`1.2%/128k` means 1.2 percent of a 128,000-token window). When the footer nears 100 percent, type `/compact` to shorten the conversation ([Compaction](/docs/latest/compaction)).

## Undo

`summaries.md` is the only change. Delete it:

```bash
rm summaries.md
```

Your PDFs were never written to. The converter reads them and prints text.

## Keep your privacy in mind

The text of every PDF that PiG converts goes to your model provider, and the conversation is saved under `~/.pig/agent/sessions/`. Start PiG with `--no-session` for a conversation that is not saved. See [Before you start](/docs/latest/applied-use-cases#before-you-start-how-pig-works-on-your-files).
