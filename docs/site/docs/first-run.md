# Your first ten minutes

This page takes you from nothing installed to one finished task. It has four steps: install, choose a model, run a walkthrough and leave. Each step shows what PiG prints.

PiG's first start asks three short questions (theme, header picture and usage data). It does not help you pick a model or run a first task. This page is the guide for those steps. It covers the same ground as Pi 1.3.0's quickstart ([Pi's quickstart](https://github.com/earendil-works/pi/blob/v1.2.0/packages/coding-agent/docs/quickstart.md)), with PiG's commands and folders.

## 1. Install

You type every command on this page into a terminal. A terminal is the program that runs typed commands: Terminal on macOS (in Applications, then Utilities), or the terminal program of your Linux desktop. On Windows, follow [Windows](/docs/latest/windows) first. Type or paste one command, then press `Enter` to run it.

Pick one of the two install commands. The first needs `curl` and works on macOS and Linux. The second needs Node.js 18 or newer (from https://nodejs.org):

```bash
curl -fsSL https://pi-in-go.dev/install.sh | sh
```

```bash
npm install -g @pi-in-go/pig
```

The first command downloads the newest published release, checks its SHA-256 checksum, and puts the `pig` program in the folder `~/.local/bin`. `~` is your home folder. It ends with a line such as:

```text
pig-install: Installed 0.5.0+1.2.0 to /home/you/.local/bin/pig
```

Your terminal finds a program only when its folder is on your `PATH`. `PATH` is the list of folders the terminal searches for commands. When `~/.local/bin` is not on your `PATH`, the installer prints one more line that tells you what to run, for example:

```text
pig-install: Add /home/you/.local/bin to PATH to run pig, for example: export PATH="/home/you/.local/bin:$PATH"
```

Run the `export` command it shows. That command lasts until you close the terminal. To keep it, add the same line to the end of your shell's startup file: `~/.zshrc` on macOS, `~/.bashrc` on most Linux systems. Then open a new terminal.

Check that the command works:

```bash
pig --version
```

PiG prints its version and the Pi version it follows. The text on your computer shows a different number when your release differs:

```text
0.5.0+1.2.0
```

The [Quickstart](/docs/latest/quickstart) lists the other install routes. [Install troubleshooting](/docs/latest/install-troubleshooting#pig-command-not-found) helps when the terminal answers `pig: command not found`.

## 2. Choose a model

PiG needs a model from a provider. Two things are needed: an account, and a way to tell PiG about it. List the accounts PiG can sign in to:

```bash
pig login --list
```

```text
Authentication targets:
  anthropic	Anthropic
  github-copilot	GitHub Copilot
  openai-codex	OpenAI (ChatGPT Plus/Pro)
  kimi-coding	Kimi For Coding
  meta	Meta (Muse subscription)
  openai	OpenAI (ChatGPT subscription)
  openrouter	OpenRouter
  radius	Radius
  xai	xAI
```

Your list can differ with the release. The list is the sign-in targets only. API-key providers are separate: see [Providers](/docs/latest/providers) for every provider and the environment variable each one reads.

Before you sign in, `pig --list-models` prints `No models available. Use /login to log into a provider via OAuth or API key.` and the paths of two help pages.

Start PiG in any folder:

```bash
pig
```

The first time, PiG shows a setup screen with three questions. Press `Enter` to accept each one, or `Esc` to skip all of them and save no answer. PiG does not show the screen again either way:

```text
 Welcome to PiG, the minimal coding agent.

 Pick a theme.

 → System (matches your terminal colors)
   Dark
   Light

 ↑↓ navigate  enter continue  escape/ctrl+c skip setup
```

1. **Pick a theme.** `System` uses your terminal's colours.
2. **Pick a sprite.** The picture of a pig at the top of the screen. It changes nothing else. Change it later with `/sprite`.
3. **Opt-in to anonymous usage data sharing?** Use `↓` to move to `Don't share` if you do not want to send usage data, then press `Enter`. To change it later, set `"enableAnalytics"` to `true` or `false` in `~/.pig/agent/settings.json`. The screen's text also mentions a privacy command. Neither Pi 1.3.0 nor PiG has one. PiG shows Pi's text with only the program name changed (Pi `first-time-setup.ts:77`).

PiG saves your answers in `~/.pig/agent/settings.json` and does not ask again. Pi shows this screen only for its own release with `PI_EXPERIMENTAL=1` set. PiG shows it on the first interactive start (divergence D88, `coding/cli/extensions.go:93-95`). PiG skips the screen when the `PIG_CODING_AGENT_DIR` environment variable is set, and when it runs a single prompt with `-p`.

On its first start PiG also downloads two search programs it uses to find files, and prints `fd not found. Downloading...` and `ripgrep not found. Downloading...` (Pi `utils/tools-manager.ts:375`). This happens once. Then it warns `No models available` above the editor until you sign in.

Type `/login` and press `Enter`:

```text
/login
```

`/login` shows:

```text
 Select authentication method:

 → Sign in with an account
   Sign in with an API key
   Sign in with Radius • not configured

 ↑↓ navigate  enter select  escape/ctrl+c cancel
```

- **Sign in with an account** is for a subscription you already pay for, such as ChatGPT or GitHub Copilot. PiG then takes you through that provider's own sign-in.
- **Sign in with an API key** is for a key from a provider's website. PiG lists the providers. Type part of the provider's name to shorten the list, then press `Enter`. PiG asks for the key and masks what you type: dots, a character count and the last four characters (PiG's `maskSecretInput` setting, divergence D80; Pi 1.3.0 shows the key as typed). It stores the key in `~/.pig/agent/auth.json`, readable by your user only ([Providers](/docs/latest/providers#authentication)).

PiG does not test the key when you save it. It confirms the save and picks a model for you, for example:

```text
 Saved API key for OpenCode Zen. Selected kimi-k2.6. Credentials saved to /home/you/.pig/agent/auth.json
```

To choose a different model, press `Ctrl+L` or type `/model` to open the model list. Type part of a model's name to shorten the list, and move to the model you want:

- `Enter` uses the model for this conversation only. The next `pig` starts with the earlier default again.
- `Ctrl+S` uses the model and makes it the default for every later start. PiG prints `Default model: <provider>/<model>`.

The list footer says the same: `Enter to select · Ctrl+S to set as default` (Pi `model-selector.ts:142` and `interactive-mode.ts:5363`).

The footer at the bottom of the screen names the model in use. Its left side counts this conversation's use: `↑` tokens sent, `↓` tokens received, `R` and `W` tokens read from and written to the provider's cache, `CH` the share of the last request the cache served, `$` the cost so far (`(sub)` when a subscription pays), and `2.1%/200k` how full the model's context window is. `(auto)` means PiG shortens the conversation by itself when the window fills up (Pi `footer.ts:180-200`).

You can check from outside PiG that a provider is ready. The command prints `ready` and exits 0, or prints `not_ready` and exits 1:

```bash
pig auth check --provider openai
```

```text
not_ready
```

List every model PiG can use with the credentials it has, and filter by a word:

```bash
pig --list-models
pig --list-models sonnet
```

```text
provider  model   context  max-out  thinking  images
mock      mock-1  128K     4.1K     no        no
```

The table above came from a test model. Your table lists the models of your own providers. To start with one model, pass it as `provider/model`:

```bash
pig --model anthropic/claude-sonnet-4-5
```

## 3. Run one walkthrough

Make a small practice folder, so nothing you care about is at risk. The three commands below make a folder named `pig-practice` in your home folder, move the terminal into it (`cd` means "change directory"), write a short notes file named `notes.txt`, and start PiG there. PiG works on the files in the folder where you start it:

```bash
mkdir -p ~/pig-practice && cd ~/pig-practice
printf 'Week 12 notes\n- API migration finished, 2 days late\n- Hired one designer (starts Monday)\n- Budget: 8%% under plan\n- Risk: vendor contract renewal due 30 Oct\n' > notes.txt
pig
```

Type this and press `Enter`:

```text
Write a one-page status update for my manager from notes.txt into update.md. Plain language, three short paragraphs.
```

PiG prints your message, then each step the model takes. A file written by the model appears as `write <name>` followed by the text it wrote, and a shell command appears as `$ <command>` followed by its output. Most models first open the notes, shown as `read notes.txt`. The screen below shows the `write` step and the model's reply:

```text
 write update.md

 # Project update, week 12

 The API migration finished two days late. The team is now working on the remaining clean-up.

 A designer joins on Monday. Hiring is complete for this quarter.

 The project is 8% under budget. The one open risk is the vendor contract, which renews on 30
 October.

 I wrote update.md from notes.txt: three short paragraphs (progress, people, budget and risk).
```

The wording on your screen differs. Open `update.md` in any editor to read the result. To undo, delete `update.md`.

The longer walkthroughs start from [Applied use cases](/docs/latest/applied-use-cases).

## 4. Leave and come back

Press `Ctrl+D` on an empty editor. PiG prints how to return to the conversation:

```text
To resume this session: pig --session 01a12bb7-80df-773c-8928-031c941bb0df
```

(`internal/codingagent/interactive.go:1420`; Pi prints the same line at `interactive-mode.ts:4338`.)

Run `pig --continue` in the same folder to resume the most recent conversation. `pig --resume` lists the saved ones. See [Sessions](/docs/latest/sessions).

## If something goes wrong

| What you see | What it means | What to do |
|---|---|---|
| `No API key found for <provider>.` | No credentials for that provider. | Sign in with `/login`, or set the provider's key variable. |
| `Error: 401` with `Invalid credential` or `Unauthorized`, after your first prompt | The provider refused the key, or your plan does not include this model. `/login` does not test a key when it saves it. | Check the key on the provider's website and sign in again with `/login`, or pick another model with `/model`. |
| `No models available` | No provider is signed in. | Sign in with `/login`. |
| `pig: command not found` | The install folder is not on your `PATH`. | [Install troubleshooting](/docs/latest/install-troubleshooting#pig-command-not-found). |
| The model keeps going | The task is large or unclear. | Press `Esc`, then tell it what to do next. |
