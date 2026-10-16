# Skills

A skill is a block of task-specific instructions you load into a session. An extension adds a *capability* (a tool, a command); a skill adds *knowledge*: how to do a particular job well, in your own words, without writing code. PiG tells the model which skills exist, and the model reads a skill's instructions when the task matches it. You can also load one yourself with `/skill:<name>`.

Use a skill when the thing you want to reuse is a procedure or a convention, not a new tool: your commit-message format, how to run this repo's tests, the steps for cutting a release.

## What a skill is on disk

A skill is a directory named for the skill, containing a `SKILL.md` file:

```text
~/.pig/agent/skills/commit/SKILL.md
```

`SKILL.md` is markdown with a YAML frontmatter header:

```markdown
---
name: commit
description: Write a commit message in this repo's format.
---

Write commits as `type(scope): summary`. Keep the summary under 72 chars.
Explain *why* in the body, not what. Never mention tooling.
```

- **`name`** - the skill's identifier (defaults to the directory name).
- **`description`** - a one-line summary.
- **body** - the instructions. PiG does not put the body in the system prompt. It lists each skill's name, description and file location there, and the model opens the file with its `read` tool when it needs the body. When the `read` tool is not available to the model, the hint names no tool. `/skill:<name>` puts the body in your message (see the example below).

Set `disable-model-invocation: true` in the frontmatter when a skill should be available only through its `/skill:<name>` command. The skill then stays out of the system prompt list.

## Load a skill

Explicitly, for one run:

```bash
pig --skill commit
pig --skill ./path/to/skill-dir      # a direct path also works
```

From a piglet, so it loads every time that agent runs (see [piglets](/docs/latest/piglets)):

```yaml
skills:
  - name: commit
    origins: [local:./skills/commit]
```

Every skill entry in a Piglet needs `origins`, usually a declared `package:<alias>` member or `local:<relative-path>`. A bare name has no origin, and `pig piglet validate` rejects it with `FAIL: skill "commit": no origin specified` (`coding/piglet/origins.go:262`).

## Discovery

Beyond explicit `--skill` and Piglet entries, PiG can discover Skills already present in a project or your home directory. Treat discovery as a local convenience, not as the dependency contract for an agent you hand to someone else. Portable Piglets declare Skill origins.

PiG scans:

- `~/.pig/agent/skills/` for your own skills,
- `~/.agents/skills/` for user-level skills shared with other agents,
- `.agents/skills/` at each directory from the workspace up to the repository root, or the filesystem root outside a repository,
- `.pig/skills/` in the workspace.

`~/.pig/skills/` (without `agent`) is not scanned. A skill there is silently ignored. Project skills load only after you trust the project ([Security](/docs/latest/security)).

The `skills` setting adds files and directories and filters discovered skills ([Settings](/docs/latest/settings)). A leading `~` expands to your home directory in plain entries and in `!`, `+`, `-` and wildcard entries, for example `"!~/claude-backup/skills/pdf"`, as in Pi 1.3.0. Pi 1.1.0 matched such a pattern against the literal text, so it matched nothing (Pi #10765).

A piglet controls whether this scan runs, so a locked-down agent picks up nothing it did not declare:

```yaml
discovery:
  skills: [workspace]  # project Package/convention skills only
```

## Worked example

Create the skill from the format above:

```bash
mkdir -p ~/.pig/agent/skills/commit
cat > ~/.pig/agent/skills/commit/SKILL.md <<'EOF'
---
name: commit
description: Write a commit message in this repo's format.
---

Write commits as `type(scope): summary`. Keep the summary under 72 chars.
Explain *why* in the body, not what. Never mention tooling.
EOF
```

Start PiG and type `/skill:`. The list shows each skill with its scope: `[u]` for your own, `[p]` for the project's. Checked on PiG, with two project skills and two user skills:

```text
→ skill:cat     [p] Skill cat.
  skill:dog     [p] Skill dog.
  skill:commit  [u] Write a commit message in this repo's format.
  skill:amy     [u] Skill amy.
```

Type `/skill:commit write one for my change` and press `Enter`. PiG sends the skill's body with your words. The model receives this message (checked in the provider request):

```text
<skill name="commit" location="/home/you/.pig/agent/skills/commit/SKILL.md">
References are relative to /home/you/.pig/agent/skills/commit.

Write commits as `type(scope): summary`. Keep the summary under 72 chars.
Explain *why* in the body, not what. Never mention tooling.
</skill>

write one for my change
```

Without the `/skill:` prefix, the model still knows the skill exists, because the system prompt carries this block, and it can read the file when your request matches the description:

```text
<available_skills>
  <skill>
    <name>commit</name>
    <description>Write a commit message in this repo&apos;s format.</description>
    <location>/home/you/.pig/agent/skills/commit/SKILL.md</location>
  </skill>
</available_skills>
```

`--no-skills` leaves the block out and removes the `/skill:` commands. `enableSkillCommands: false` removes the commands only ([Settings](/docs/latest/settings)).

## Share a skill

Skills travel the same way as every other resource: bundle them in a [package](/docs/latest/packages) and `pig install` it, or publish them to a marketplace. A package can carry skills alongside extensions, prompts, and themes.

## See also

- [concepts](/docs/latest/concepts) - where skills sit among resources, packages, and piglets.
- [piglets](/docs/latest/piglets) - declaring a skill set as part of an agent, with discovery toggles.
- [packages](/docs/latest/packages) - sharing and installing skills.
- [upstream Pi's skills documentation](https://pi.dev/docs/latest/skills) - the reference implementation's skill format and locations.
