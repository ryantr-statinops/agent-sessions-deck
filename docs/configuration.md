# Configuration

`asd` reads one strict YAML config at `$XDG_CONFIG_HOME/asd/config.yaml`
(default `~/.config/asd/config.yaml`). If the file is absent, the documented
defaults below apply; if it is present and invalid, `asd` fails loudly and
never falls back to defaults mid-run.

## Layout (ADR 0003)

| Kind | Path | Notes |
|---|---|---|
| Config | `$XDG_CONFIG_HOME/asd/config.yaml` | Strict parse; unknown keys rejected. |
| State | `$XDG_STATE_HOME/asd/` | `sessions.json`, `state.json`, `.lock`. |
| Runtime | `$XDG_RUNTIME_DIR/asd/` | Control socket; fallback path is decided at Stage 06. |

Persistent state is JSON with `schema_version`/`revision` envelopes; `sessions.json` owns the full session/attempt snapshot and `state.json` owns `recent_workspaces`. The two files are never mutated in one transaction.

## Schema

```yaml
refresh: 2s
discovery:
  extra_paths: [~/.local/bin, /opt/agents/bin]
agents:
  - id: opencode
    name: OpenCode
    executable: /usr/local/bin/opencode
    args: []
workspaces:
  - path: ~/Kestrel
terminal:
  scrollback: 10000
  max_input_queue: 65536
```

Defaults: `refresh: 2s`, `terminal.scrollback: 10000`, `terminal.max_input_queue: 65536`, empty agents/workspaces/extra_paths.

Validation rules (all errors name the file and key):

- unknown keys are refused (strict parse);
- duplicate keys at any mapping level are refused;
- a config document must contain exactly one YAML document;
- `refresh` must parse as a duration (e.g. `2s`);
- agent IDs must satisfy the stable-ID grammar and be unique;
- `name`, `executable`, and workspace `path` must be non-empty;
- `terminal.scrollback` and `terminal.max_input_queue` must be positive.

## Command contract

`executable` plus `args` is a literal argv array. It is never shell-split,
globbed, eval'd, or re-parsed: an argument like `$(touch /tmp/x)` stays a
literal argument. Display paths prefer redaction (`executable [redacted]`)
because argv may carry secrets; command lines are not logged by default.

## Path and permission policy

- Explicit `~/...` expands to the user's home; `~user` forms are refused.
- State and config directories are created `0700`; new state/config files are `0600`. Existing user files keep their modes; `asd` never chmods them destructively without notice.
- State home is single-writer: the owner holds `.lock` (flock) for its lifetime; offline mutations take it briefly and are refused while an owner is live.
- Secrets are never persisted: no environment dumps, no tokens, no raw terminal output in `state.json`.

## Legacy migration

If `~/.config/asd/sessions.json` exists and the XDG `sessions.json` does not, the legacy file (Stage 02 wire format) is strictly decoded, backed up next to the source, imported into the schema-v1 envelope with revision 1, and the source is preserved. A valid XDG destination is always authoritative; corrupt or future-version state is preserved and fails with recovery guidance, never silently reset.

## See also

- `config.example.yaml` — a complete example that parses under the strict schema.
- [ADR 0003](architecture/adr/0003-state-and-storage.md) — the frozen layout/persistence contract.
