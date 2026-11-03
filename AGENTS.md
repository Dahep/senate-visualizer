## Agent skills

### Issue tracker

GitHub Issues for Dahep/senate-visualizer, managed with the `gh` CLI. See `docs/agents/issue-tracker.md`.

### Triage labels

Five canonical triage roles as default label strings (`needs-triage`, `needs-info`, `ready-for-agent`, `ready-for-human`, `wontfix`). See `docs/agents/triage-labels.md`.

### Domain docs

Single-context: root `CONTEXT.md` + `docs/adr/`. Per the `domain-modeling` skill.

### Model pins

Subagent dispatches must resolve an explicit pinned model or agent — a bare `task` call silently inherits the parent's model. Read the dispatch roster (billing split, pins, overrides) in the stowed machine-wide AGENTS.md (`.config/opencode/AGENTS.md` in the dotfiles repo); pinned agent defs live in `~/.config/opencode/agent/*.md`. Pin changes go through the `model-rotation` skill, never ad-hoc edits.
