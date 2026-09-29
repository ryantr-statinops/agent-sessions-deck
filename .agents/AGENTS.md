# Agent resources for this repository

`.agents/` contains the repository-local skill library and the installed Orca orchestration discovery skill. The product and implementation plan remain the source of truth for scope, requirements, and acceptance; skills are optional operating procedures, not product requirements.

## Contents

- [`SKILLS/`](SKILLS/README.md): complete `ryantr-statinops/SKILLS` repository imported with Git subtree from `git@github.com:ryantr-statinops/SKILLS.git`, branch `main`. The imported upstream revision is recorded in the subtree merge commit. It includes common and personal skills, workflows, bundles, evaluation assets, documentation, and scripts. Treat this directory as upstream-managed; do not make local edits inside it unless intentionally preparing an upstream contribution.
- [`skills/orchestration/SKILL.md`](skills/orchestration/SKILL.md): copy of the machine-installed Orca orchestration skill. It is a discovery stub: for Orca commands, resolve the correct CLI and load its version-matched guide as directed by that file. Do not replace it with generic agent orchestration or invoke the Orca binary for unrelated work.

## Discover and use SKILLS

1. Start with [`SKILLS/README.md`](SKILLS/README.md), `SKILLS/docs/skill-index.md`, and `SKILLS/data/promoted.json` to find current, supported candidates. Check the relevant bundle in `SKILLS/data/bundles.json` when the work matches a broader outcome such as `feature-delivery` or `bug-fixing`.
2. Read the selected skill's `SKILL.md` before applying it. Skill files are guidance; follow the project plan, current repository instructions, and user scope when they differ.
3. Prefer `SKILLS/common/` for reusable practices. `SKILLS/personal/` contains user-specific preferences and workflows; apply only when they fit this project and task.
4. The plan maps a small set of candidate skills to each stage. Select only skills relevant to the work; do not load or install the entire catalog into a runtime directory just because it is vendored here.
5. Preserve existing files and validate changes using the stage acceptance criteria.

## Updating the vendored library

Update the subtree intentionally from its upstream `main` branch:

```bash
git subtree pull --prefix=.agents/SKILLS git@github.com:ryantr-statinops/SKILLS.git main
```

Review the resulting subtree change and update this guidance or plan references only when the upstream layout or skill identifiers change. Do not replace the subtree with a nested clone or a gitlink.
