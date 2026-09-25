# Windows setup

PiG supports source builds on Windows. Do not treat Windows as release-supported until native release verification passes for the published artifact.

## Build from source

Install Git and Go 1.27.1. Then run:

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

## Terminal

Use Windows Terminal or another terminal that supports the required input and ANSI behavior.

Ctrl+Enter can provide multiline input where Shift+Enter is intercepted by the terminal. See [Terminal setup](/docs/latest/terminal-setup).

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

`pig update` updates an npm or pnpm installation through its package manager, as Pi does. Before an npm update, PiG moves the running `pig.exe` into `node_modules\.pig-native-quarantine` and copies it back, because Windows cannot delete a running program. The next start removes the quarantine. PiG refuses to self-update a yarn or bun installation on Windows, as Pi does.

PiG does not replace a standalone `pig.exe` in place (D39). Download the new release and replace the file.

## Verification

Run the Windows-native unit and integration gates before claiming support for an artifact. A Linux cross-build alone does not prove Windows terminal, process, filesystem, update, or extension behavior.

## Upstream Pi

See [upstream Pi's Windows documentation](https://pi.dev/docs/latest/windows) when you need the TypeScript reference implementation.
