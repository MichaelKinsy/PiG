# Automate a weekly report

**Goal:** have PiG write the same report from fresh data every week, without you typing anything. You do the task once by hand, save the instruction as a small script and schedule the script.

**You need:** PiG signed in with `pig login` ([Your first ten minutes](/docs/latest/first-run)), a data file that changes weekly (CSV or text) and a Mac or Linux computer. The script is a shell script. On Windows, run it with WSL or Git Bash.

**Time:** fifteen minutes to set up. Each weekly run takes as long as one model reply.

## How it works

`pig -p "<prompt>"` runs one prompt and exits, with no screen to watch and nothing to type. That is print mode. The pieces of the command are:

| Piece | Meaning |
|---|---|
| `-p` | Run the prompt once and exit (Pi `packages/coding-agent/src/cli/args.ts:177`). |
| `--no-session` | Do not save the conversation (Pi `packages/coding-agent/src/cli/args.ts:132`). Without it, every run adds a file under `~/.pig/agent/sessions/`. |
| `--tools read,write` | Allow only the tools `read` and `write`. The model cannot run commands or edit other files (Pi `packages/coding-agent/src/cli/args.ts:151`). |
| `@this-week.csv` | Put this file's contents in the message (Pi `packages/coding-agent/src/cli/file-processor.ts:78`: the text arrives as `<file name="...">`). |
| the quoted text | The instruction. |

## Step 1: do it once by hand

Make a folder with the weekly data and start PiG there:

```bash
mkdir ~/weekly && cd ~/weekly
printf 'day,orders,refunds\nMon,41,2\nTue,38,1\nWed,52,4\nThu,47,0\nFri,60,3\n' > this-week.csv
pig
```

```text
Read this-week.csv. Write weekly-report.md with three lines: total orders, total refunds with the refund rate as a percentage, and the best day by orders.
```

Check `weekly-report.md` against the numbers. Here the orders total 238 and refunds total 10, which is 4.2 percent, and the best day is Friday. Adjust the prompt until the report is what you want. The final prompt is what you will schedule, so write it so that a stranger could follow it.

## Step 2: save it as a script

Save this as `~/weekly/weekly-report.sh`. Replace `/usr/local/bin/pig` with the output of `command -v pig`:

```bash
#!/bin/sh
# Writes weekly-report.md from this-week.csv. Appends everything PiG prints to weekly-report.log.
cd "$(dirname "$0")" || exit 1
/usr/local/bin/pig -p --no-session --no-extensions --tools read,write @this-week.csv \
  "Write weekly-report.md: total orders, total refunds, refund rate and the best day. Three lines." \
  >> weekly-report.log 2>&1
```

Make it runnable and run it:

```bash
chmod +x ~/weekly/weekly-report.sh
~/weekly/weekly-report.sh
```

PiG prints the model's final reply into `weekly-report.log` and writes `weekly-report.md`:

```text
Report written to weekly-report.md.
```

```text
# Weekly report

Orders: 238. Refunds: 10 (4.2%). Best day: Friday (60 orders).
```

The script's exit code is PiG's exit code. It is 0 when the run worked. A model that cannot be reached writes its error to the log and the script exits 1. Here is the whole log of a run when the provider could not be reached:

```text
Connection error.
```

Look in `weekly-report.log` first when a report is missing.

### Why the full path and a stored login

A scheduler starts the script with an almost empty environment. It has no `PATH` entry for your PiG, and it does not read your shell profile, so a key you `export`ed in `~/.zshrc` is not there. Two rules follow:

1. Write the full path to `pig`, as above.
2. Sign in with `pig login`. PiG stores that credential in `~/.pig/agent/auth.json` and reads it in every run (`providers.md`: stored credentials take precedence over the environment). If you use an API key from the environment, put the `export` line in the script itself, above the `pig` line, and protect the script (`chmod 700`).

The script was tested under `env -i PATH=/usr/bin:/bin HOME=...`, an almost empty environment like the scheduler's.

The scheduler's `PATH` also has no Node.js. PiG cannot load an extension written in TypeScript or JavaScript there, and the run would stop with `Error: Failed to load extension ... node was not found on PATH`. The report needs no extensions, so the script starts PiG with `--no-extensions`.

## Step 3: schedule it

`crontab -e` opens your schedule. Add one line. This one runs every Monday at 08:00:

```text
0 8 * * 1 /home/you/weekly/weekly-report.sh
```

The five fields are minute, hour, day of month, month and day of week (`1` is Monday). Check the line you saved:

```bash
crontab -l
```

On a Mac, `cron` may need Full Disk Access (System Settings, Privacy & Security) before it can read folders such as Documents. Test the schedule by setting the line to run in two minutes, then set it back.

## Step 4: keep it safe

- **Keep the tool list small.** `--tools read,write` is the point of the script. A scheduled run has nobody watching, so give it only the tools it needs. Leave out `bash`.
- **One output file.** The prompt names `weekly-report.md`. `write` replaces a file of that name each week. Keep last week's with a dated copy: add `cp weekly-report.md "report-$(date +%F).md"` as a line before the `pig` line.
- **Read the first few reports.** A model can misread a column. Spot-check against the data until you trust the prompt.
- **No data, no report.** A missing `@` file stops PiG before any model call: it prints `Error: File not found: <path>` and exits 1, and the log shows it (Pi `packages/coding-agent/src/cli/file-processor.ts:36-40`). An **empty** file is skipped without any message (`packages/coding-agent/src/cli/file-processor.ts:46-49`), so the model gets your instruction and no data, and may make numbers up. Add `[ -s this-week.csv ] || exit 1` after the `cd` line to stop on both.

## Undo

Remove the schedule line (`crontab -e`, delete the line) and delete `weekly-report.sh`. The script wrote only `weekly-report.md` and `weekly-report.log`, because it has no tool to write anywhere else. It could still write to a path the prompt names, so keep paths in the prompt local.

## Variations

- **A report from several files:** `@sales.csv @refunds.csv` in the same command.
- **A longer instruction:** keep it in a file and use `@prompt.md` in place of the quoted text.
- **A different model for this job:** add `--model provider/model`, for example a smaller and cheaper one.
