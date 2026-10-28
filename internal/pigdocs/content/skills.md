# Skills

> **Differs from Pi, or easy to get wrong**
> - User skills live in `~/.pig/agent/skills/` or `~/.agents/skills/`. PiG does not scan `~/.pig/skills/` or `~/.pi`.
> - Project skills (`.pig/skills/`, `.agents/skills/`) load only in a trusted project. With `-p` pass `--approve`.

A skill is a block of task-specific instructions for a session. An extension adds a *capability* (a tool, a command); a skill adds *knowledge*: how to do a particular job well, in your own words, without writing code. PiG tells the model each skill's name, description, and path. The model reads the full instructions only when the task needs them.

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

- **`name`** - the skill's identifier (defaults to the directory name). Use lowercase letters, digits, and hyphens, at most 64 characters, with no leading, trailing, or doubled hyphen.
- **`description`** - required. It says what the skill does and when it applies. The model uses it to decide when to read the skill, so write both parts. A skill with no description, or an empty one, is left out of the prompt, and the session still starts. Keep it to 1024 characters or fewer.
- **body** - the instructions. PiG does not put the body in the system prompt. The model reads this file when the description matches the task.

## Load a skill

Explicitly, for one run:

```bash
pig --skill commit
pig --skill ./path/to/skill-dir      # a direct path also works
```

From a piglet, so it loads every time that agent runs (see [piglets](piglets.md)):

```yaml
skills:
  - name: commit
    origins: [local:./skills/commit]
  - learn-codebase        # a bare name resolves through the skills dir
```

For a portable Piglet, use ordered typed `origins`, usually a declared
`package:<alias>` member or `local:<relative-path>`. A bare name is convenient
on your machine but depends on a local skill already being installed under the
config tree (`~/.pig/agent/skills/<name>/SKILL.md`).

## Discovery

Beyond explicit `--skill` and piglet entries, pig can discover skills already sitting in a project or your home dir. Treat discovery as a local convenience, not as the dependency contract for an agent you hand to someone else: portable piglets should declare skill origins.

PiG scans:

- `~/.pig/agent/skills/` for user-level skills,
- `~/.agents/skills/` for user-level skills in the Agent Skills location,
- `.agents/skills/` at each directory from the workspace up to its root,
- `.pig/skills/` in the workspace.

PiG does not scan `~/.pig/skills/`. Project locations load only in a trusted project ([project trust](trust.md)).

A piglet controls whether this scan runs, so a locked-down agent picks up nothing it did not declare:

```yaml
discovery:
  skills: [workspace]  # project Package/convention skills only
```

## Share a skill

Skills travel the same way as every other resource: bundle them in a [package](packages.md) and `pig install` it, or publish them to a marketplace. A package can carry skills alongside extensions, prompts, and themes.

## See also

- [concepts](concepts.md) - where skills sit among resources, packages, and piglets.
- [piglets](piglets.md) - declaring a skill set as part of an agent, with discovery toggles.
- [packages](packages.md) - sharing and installing skills.
