# Windows setup

PiG supports source builds on Windows. Do not treat Windows as release-supported until native release verification passes for the published artifact. The command environment follows Pi 1.4.0 ([Pi's Windows guide](https://github.com/earendil-works/pi/blob/v1.2.0/packages/coding-agent/docs/windows.md)).

## Build from source

Install Git and Go 1.27.3. Then run:

```powershell
git clone git@github.com:MichaelKinsy/PiG.git
Set-Location PiG
go build -o bin\pig.exe .\cmd\pig
.\bin\pig.exe --version
```

Windows support is a preview. Instead of building from source, install with `npm install -g @pi-in-go/pig` or `go install github.com/MichaelKinsy/PiG/cmd/pig@latest`, or download the `windows-amd64` or `windows-arm64` zip from [GitHub Releases](https://github.com/MichaelKinsy/PiG/releases). The `install.sh` installer supports macOS and Linux only.

## Configuration paths

PiG stores configuration below the user profile by default:

```text
%USERPROFILE%\.pig
%USERPROFILE%\.pig\agent
```

Set `PIG_HOME` to use an isolated configuration root. Set `PIG_CODING_AGENT_DIR` only when you need to override the agent directory.

## What was checked for this page, and what was not

PiG's Windows support is a preview, and this page was prepared on Linux. The table says which statements were observed and which are read from the code. Nothing in it was run on a Windows machine.

| Statement | How it was checked |
|---|---|
| The source builds for Windows on both architectures | Observed: `GOOS=windows GOARCH=amd64 go build -o pig.exe ./cmd/pig` took 28 s and `file pig.exe` printed `PE32+ executable (console) x86-64, for MS Windows`. The same build with `GOARCH=arm64` printed `PE32+ executable (console) Aarch64, for MS Windows`. `pig.exe` was 90,811,904 bytes. |
| `pig.exe` runs | **Not run.** A cross-build proves the program links for Windows, not that it starts (see [Verification](#verification)). |
| The configuration root | Observed on Linux with the same code, which does not branch on the operating system (`internal/configroot/configroot.go:50-67`): see the next section. |
| Editor order | Read from `resolveExternalEditorCommand` in `internal/codingagent/settings.go`: a non-blank `externalEditor` setting, then `VISUAL`, then `EDITOR`, then `notepad` on Windows (`nano` on every other system). Not run on Windows. |
| The npm update quarantine | Read from `internal/codingagent/windows_self_update.go` (`quarantineDirName`, `.pig-native-quarantine`). Not run on Windows. |

### Where the configuration lives: observed

`PIG_HOME`, `XDG_CONFIG_HOME` and the profile directory are tried in that order. Pi has none of these: it always uses `~/.pi/agent` unless `PI_CODING_AGENT_DIR` is set (Pi `config.ts:605-611`). PiG's own configuration root is divergence D2. Each command below ran `pig auth check --provider openai` once with a fresh, empty home directory and printed where `auth.json` appeared (paths shortened). The Windows profile directory plays the part of `HOME`:

```text
HOME=h                                         → h/.pig/agent/auth.json
HOME=h XDG_CONFIG_HOME=x                       → x/pig/agent/auth.json
HOME=h XDG_CONFIG_HOME=x PIG_HOME=iso          → iso/agent/auth.json
HOME=h PIG_HOME=iso PIG_CODING_AGENT_DIR=a     → a/auth.json
```

On Windows `HOME` is `%USERPROFILE%`. So the default is `%USERPROFILE%\.pig\agent` only when `XDG_CONFIG_HOME` is not set. If you set `XDG_CONFIG_HOME` for another tool, PiG will use `%XDG_CONFIG_HOME%\pig\agent` and your credentials will not be where the first section says. `PIG_CODING_AGENT_DIR` replaces the agent directory only: it is the `agent` folder itself, so `auth.json` sits directly in it. See [Environment variables](/docs/latest/environment-variables).

## Choose native Windows or WSL

PiG runs either as a native Windows process or inside Windows Subsystem for Linux (WSL). Native Windows uses Git Bash by default for Bash commands and can optionally expose PowerShell to the model. PiG inside WSL uses the Linux environment and its Bash installation.

| Environment | Command environment | Use it when |
|---|---|---|
| Native Windows with Git Bash | Git Bash for the built-in `bash` tool and `!` commands | Your files and development tools primarily live on Windows |
| Native Windows with the `powershell` tool | PowerShell for model tool calls; Bash remains available for `!` commands | The task depends on PowerShell modules or Windows-native commands |
| WSL | Linux Bash and tools inside the selected WSL distribution | Your files and toolchain already live in Linux or WSL |

## Use Git Bash on native Windows

For most native Windows users, installing [Git for Windows](https://git-scm.com/download/win) is sufficient.

PiG resolves Bash in this order:

1. `shellPath` from `~/.pig/agent/settings.json`.
2. Git Bash at `%ProgramFiles%\Git\bin\bash.exe`.
3. Git Bash at `%ProgramFiles(x86)%\Git\bin\bash.exe`.
4. `bash.exe` on `PATH`, including Cygwin, MSYS2, or legacy WSL Bash.

Type this command in PiG to check the shell:

```text
!printf 'Bash is working\n'
```

If PiG cannot find Bash, it reports the Git Bash locations it checked. Install Git for Windows, put another Bash executable on `PATH`, or configure `shellPath`.

## Let the model use PowerShell

The `powershell` tool is off by default and exists only when PiG runs as a native Windows process. It runs `pwsh.exe` when available, then falls back to Windows PowerShell. It starts PowerShell with `-NoProfile -NonInteractive -ExecutionPolicy Bypass`. Administrator-enforced execution policies can still take precedence.

PowerShell discovery checks these locations in order:

1. `pwsh.exe` on `PATH`
2. `%ProgramFiles%\PowerShell\7\pwsh.exe`
3. `powershell.exe` on `PATH`
4. `%SystemRoot%\System32\WindowsPowerShell\v1.0\powershell.exe`

To replace the model's `bash` tool with `powershell`, set this in `~/.pig/agent/settings.json`:

```json
{
  "defaultTools": ["-bash", "+powershell"]
}
```

`["read", "powershell", "edit", "write"]` does the same by listing the tools explicitly. Restart PiG. The `!` and `!!` commands still use Bash.

## WSL

WSL path conversion on Windows and extra Git branch polling in WSL recognize `/mnt/<drive>`, not custom mount roots.

When PiG uses the legacy WSL `bash.exe` launcher from Windows (`bash.exe` in the `System32` or `Sysnative` directory of `%SystemRoot%`), it sends commands over stdin to avoid command-line quoting problems.

## Use a custom Bash executable

Set `shellPath` when Bash is installed somewhere PiG does not discover automatically:

```json
{
  "shellPath": "C:\\cygwin64\\bin\\bash.exe"
}
```

JSON uses backslashes for escape sequences. When you write a Windows path with backslashes, write each backslash twice, as shown above.

See [Shell aliases](/docs/latest/shell-aliases) for a command prefix and the complete shell-resolution behavior.

## Configure Windows Terminal

Use Windows Terminal or another terminal that supports the required input and ANSI behavior.

Ctrl+Enter can provide multiline input where Shift+Enter is intercepted by the terminal. See [Terminal setup](/docs/latest/terminal-setup#windows-terminal) and the Windows defaults in [Keybindings](/docs/latest/keybindings#platform-differences).

## External editor

PiG selects an editor in this order:

1. `externalEditor` in `~/.pig/agent/settings.json`;
2. `VISUAL`;
3. `EDITOR`;
4. Notepad on Windows.

Use Ctrl+G to open the current editor buffer.

## Extensions

A source extension requires its implementation toolchain. A Go extension needs Go. A Rust extension needs Cargo and Rust. A Python extension needs Python. A Node extension needs Node.

Use a prebuilt Piglet Binary when you do not want a target-side Go or Rust build. Interpreted components still need their recorded runtime.

## Updating

`pig update` updates an npm or pnpm installation through its package manager, as Pi does. Before an npm update, PiG moves the running `pig.exe` into `node_modules\.pig-native-quarantine` and copies it back, because Windows cannot delete a running program. The next start removes the quarantine. If anti-virus or a search indexer has `pig.exe` open, the move waits for it, for up to 10 seconds. Pi does not wait (D99). PiG refuses to self-update a yarn or bun installation on Windows, as Pi does.

PiG does not replace a standalone `pig.exe` in place (D39). Download the new release and replace the file.

## Verification

Run the Windows-native unit and integration gates before claiming support for an artifact. A Linux cross-build alone does not prove Windows terminal, process, filesystem, update, or extension behavior.

## Upstream Pi

See [upstream Pi's Windows documentation](https://pi.dev/docs/latest/windows) when you need the TypeScript reference implementation.
