# Skills

A skill is a block of task-specific instructions you load into a session. An extension adds a *capability* (a tool, a command); a skill adds *knowledge*: how to do a particular job well, in your own words, without writing code. When a skill is loaded, its instructions become part of the agent's guidance for that session.

Use a skill when the thing you want to reuse is a procedure or a convention, not a new tool: your commit-message format, how to run this repo's tests, the steps for cutting a release.

## What a skill is on disk

A skill is a directory named for the skill, containing a `SKILL.md` file:

```text
~/.pig/skills/commit/SKILL.md
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
- **body** - the instructions, appended to the agent's system prompt under a `## Skills` heading when the skill is loaded.

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
config tree (`~/.pig/skills/<name>/SKILL.md`).

## Discovery

Beyond explicit `--skill` and piglet entries, pig can discover skills already sitting in a project or your home dir. Treat discovery as a local convenience, not as the dependency contract for an agent you hand to someone else: portable piglets should declare skill origins.

Pig scans:

- `.agents/skills/` at each directory from the workspace up to its root,
- `~/.agents/skills/` for user-level skills,
- `.pig/skills/` in the workspace.

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
