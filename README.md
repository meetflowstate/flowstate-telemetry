# flowstate-telemetry

Capture and attribute AI coding tool spend across your engineering organization. `flowstate-telemetry` configures developer machines to emit OpenTelemetry data from AI tools to [Flowstate](https://www.flowstate.inc/platform/ai-cost-attribution/), giving you project-level cost attribution, usage pattern analysis, and actionable insights to understand and control your AI cloud bills.

One command. Every tool. Full visibility.

## Why Flowstate?

AI tool costs are growing fast — Copilot seats, Claude API usage, autonomous agents — but most organizations have zero visibility into which teams or projects drive that spend. Unattributed budgets are the first to get cut.

Flowstate turns AI spend into an attributable, project-level line item:

- **Cost attribution** — Break down spend by team, project, and tool across GitHub Copilot, Claude, Cursor, Windsurf, and more
- **Usage pattern analysis** — Understand how your engineers use AI tools, identify adoption gaps, and spot cost anomalies
- **Actionable recommendations** — Get data-driven suggestions to optimize license allocation and reduce waste
- **Token-level granularity** — Track input/output token usage by tool and project for precise cost modelling

## Platform Support

| Platform        | Status            | Install Method   |
| --------------- | ----------------- | ---------------- |
| macOS (ARM/x64) | Fully supported   | Homebrew, binary |
| Linux (x64)     | Fully supported   | Binary           |
| Windows (x64)   | Not yet supported | —                |

## Supported Tools

Every tool sends OpenTelemetry over plain HTTP (OTLP/HTTP, protobuf or JSON) to the Flowstate receiver at `https://otel.flowstate.host` (`/v1/logs`, `/v1/metrics`, `/v1/traces`), authenticated with an `x-flowstate-key` header. gRPC is never configured.

| Tool         | macOS | Linux | Config Method                                          | Transport                                  |
| ------------ | ----- | ----- | ------------------------------------------------------ | ------------------------------------------ |
| Claude Code  | Yes   | Yes   | `~/.claude/settings.json` env block                    | OTLP/HTTP protobuf, delta metrics          |
| Copilot Chat | Yes   | Yes   | VS Code `settings.json` (`…otel.headers`, 1.138+)      | OTLP/HTTP JSON                             |
| Copilot CLI  | Yes   | Yes   | `copilot` wrapper function in the shell profile        | OTLP/HTTP protobuf (traces, delta metrics) |
| Gemini CLI   | Yes   | Yes   | `~/.gemini/settings.json` + `gemini` wrapper           | OTLP/HTTP JSON, delta metrics              |
| Codex CLI    | Yes   | Yes   | `~/.codex/config.toml` `[otel]`                        | OTLP/HTTP protobuf (logs, metrics, traces) |
| Qwen Code    | Yes   | Yes   | `~/.qwen/settings.json` + `qwen` wrapper               | OTLP/HTTP JSON, delta metrics              |
| Cursor       | Yes   | Yes   | Hook script + `~/.cursor/hooks.json`                   | OTLP/HTTP JSON logs                        |
| Windsurf     | Yes   | Yes   | Hook script + `~/.codeium/windsurf/hooks.json`         | OTLP/HTTP JSON logs                        |
| Antigravity  | Yes   | Yes   | Hook script + `~/.gemini/config/hooks.json`            | OTLP/HTTP JSON logs                        |
| Aider        | Yes   | Yes   | `~/.aider.conf.yml`                                    | —                                          |
| OpenCode     | Yes   | Yes   | `experimental.openTelemetry` + `opencode` wrapper      | OTLP/HTTP JSON (logs, traces)              |

Notes:

- **Where the key goes.** The key is scoped to each tool and is never exported as `OTEL_EXPORTER_OTLP_HEADERS`: every OpenTelemetry SDK reads that variable, so any other instrumented program would send your Flowstate key to its own (third-party) collector, and it would clobber a value you set yourself. Each tool gets the key from its own settings where it has one (Claude Code `env` block, Codex `[otel]` headers, Gemini CLI `telemetry.otlpHeaders` on 0.65+, Copilot Chat `github.copilot.chat.otel.headers`). Tools that only read the environment (Copilot CLI, OpenCode, Qwen Code, Gemini CLI before 0.65) get small wrapper functions in your shell profile (`copilot`, `opencode`, `qwen`, `gemini`) that set the `OTEL_*` variables for that one command; the `gemini` and `qwen` wrappers also set delta metrics temporality. Only commands started from a shell that reads the profile are covered. Variables a tool passes on to programs it runs itself are outside this tool's control.
- **Shell profile.** `install` adds a marked block exporting `FLOWSTATE_OTLP_ENDPOINT`, `FLOWSTATE_OTLP_KEY` (read by the wrappers and hook scripts; no OpenTelemetry SDK reads them) and, when `FLOWSTATE_TEAM_ID` / `FLOWSTATE_EMAIL` are set, `OTEL_RESOURCE_ATTRIBUTES`. It never sets `service.name`: each tool reports its own. No generic `OTEL_EXPORTER_OTLP_*` variable is exported.
- **Copilot Chat** takes the key only from the `github.copilot.chat.otel.headers` setting. VS Code builds without that setting (before 1.138) cannot authenticate: update VS Code. **Settings Sync** would upload that setting, and so your key, to your sync account and every synced machine; `install` adds it to `settingsSync.ignoredSettings` and `verify` warns when it is missing. If Settings Sync was on before, rotate the key.
- **Hook tools (Cursor, Windsurf, Antigravity)** send one log record per hook with ids, the model, line counts, the file extension (`code.file_extension`, lower case without the leading dot, e.g. `tsx`) and prompt length only. Prompt text, file contents, paths, commands and transcripts are never sent. Editors opened from the Dock read the endpoint and key from `~/.config/flowstate-telemetry/env` (mode 0600). The key header is passed to `curl` on stdin, never on its command line (visible in `ps`). The scripts need `jq` and `curl`.
- **OpenCode**: upstream marks `experimental.openTelemetry` as deprecated.
- **Prompt and response logging stays off.** Flowstate drops content anyway, so it should not leave the machine. Gemini CLI and Qwen Code default to logging prompts, so `logPrompts` is written as `false` explicitly. `--prompts` exists but is not recommended, and `verify` warns when a tool logs prompts.
- **Codex** exports OTLP metrics only while `[analytics] enabled` is `true` (the default); `verify` warns when it is `false`.

### Upgrading from otel.flowstate.inc

Earlier releases used the retired gRPC receiver `https://otel.flowstate.inc` and exported it as `FLOWSTATE_OTLP_ENDPOINT` from the shell profile (plus a global `OTEL_EXPORTER_OTLP_HEADERS`). Any `otel.flowstate.inc` endpoint, with or without a port or path, is treated as unset, so re-running `install --all` configures every tool for `https://otel.flowstate.host` and rewrites the shell block, the hook env file and an existing `--gui-env` LaunchAgent. The hook scripts ignore the old endpoint too. `verify` fails while the old endpoint remains anywhere (environment, shell profile, tool configs, hook env file, LaunchAgent, machine-wide files left by older MDM templates) and warns about a leftover global `OTEL_EXPORTER_OTLP_HEADERS`. Open a new terminal after installing.

## Install

### Homebrew (macOS)

```bash
brew tap meetflowstate/tap
brew install flowstate-telemetry
```

### Binary (macOS / Linux)

Download the latest release from the [releases page](https://github.com/meetflowstate/flowstate-telemetry/releases) and add it to your PATH.

### Build from source

```bash
make build
make install
```

## Quick Start

```bash
# Set your Flowstate API key (get one at https://app.flowstate.inc)
export FLOWSTATE_OTLP_KEY="your-key"

# Auto-detect and configure all installed AI tools
flowstate-telemetry install --all

# Verify everything is working
flowstate-telemetry verify
```

## Usage

### Configure all detected tools

```bash
flowstate-telemetry install --all
```

### Configure a single tool

```bash
flowstate-telemetry install --tool claude-code
```

### Expose the variables to Dock-launched apps (macOS)

```bash
flowstate-telemetry install --all --gui-env
```

Runs `launchctl setenv` for `FLOWSTATE_OTLP_ENDPOINT` and `FLOWSTATE_OTLP_KEY`, and installs `~/Library/LaunchAgents/inc.flowstate.telemetry.env.plist` to repeat it at login. It is rarely needed (the hook scripts read `~/.config/flowstate-telemetry/env`), and every GUI app you run can then see these variables, so it is opt-in. It never publishes `OTEL_EXPORTER_OTLP_HEADERS`, and unsets the copy earlier releases published. Restart running apps afterwards. `remove --all` undoes it.

### Preview changes (dry run)

```bash
flowstate-telemetry install --all --dry-run
```

The key is shown as `<redacted>`.

### Check status

```bash
flowstate-telemetry status
```

### Remove configuration

```bash
flowstate-telemetry remove --all
```

## Fleet Deployment (MDM)

For enterprise rollouts, generate deployment artefacts for your MDM platform:

```bash
flowstate-telemetry generate-mdm --format jamf      # Jamf Pro
flowstate-telemetry generate-mdm --format intune     # Microsoft Intune
flowstate-telemetry generate-mdm --format ansible    # Ansible
flowstate-telemetry generate-mdm --format puppet     # Puppet
```

- **One key per artefact.** The artefact embeds the key you generate it with, so every user it is deployed to is attributed to that key's owner. Per-user attribution needs per-user keys: substitute a per-user key at deployment (Jamf: script parameter 4 overrides the embedded key; Ansible: `flowstate_key` per host; Puppet: `$key` from Hiera; Intune: `$Key`).
- **No machine-wide key.** The templates write the key only into each user's tool settings and per-user files or variables (Jamf: `~/.config/flowstate-telemetry/env`, mode 0600, owned by the user; Ansible/Puppet: `~/.bashrc`; Intune: user-scope `FLOWSTATE_*` variables, so run it as the logged-on user). They no longer write `/etc/flowstate-telemetry.env`, `/etc/profile.d/flowstate-telemetry.sh` or `/etc/environment` entries, never set `OTEL_EXPORTER_OTLP_HEADERS` machine-, session- or shell-wide, and remove what earlier versions wrote there (Jamf also removes the old LaunchAgent and unsets the variables in the logged-in user's session). Gemini CLI gets the key from a `gemini` wrapper function in `~/.bashrc` (Ansible, Puppet).

## Environment Variables

| Variable                  | Description                                                          |
| ------------------------- | -------------------------------------------------------------------- |
| `FLOWSTATE_OTLP_ENDPOINT` | OTLP/HTTP receiver base URL (default: `https://otel.flowstate.host`) |
| `FLOWSTATE_OTLP_KEY`      | Flowstate API key for authentication                                 |
| `FLOWSTATE_TEAM_ID`       | Team identifier for resource attributes                              |
| `FLOWSTATE_EMAIL`         | User email for resource attributes                                   |

## Learn More

- [AI Cost Attribution Platform](https://www.flowstate.inc/platform/ai-cost-attribution/)
- [Flowstate Documentation](https://www.flowstate.inc)

## License

MIT
