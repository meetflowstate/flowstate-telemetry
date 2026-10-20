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
| Copilot Chat | Yes   | Yes   | VS Code `settings.json` + `OTEL_EXPORTER_OTLP_HEADERS` | OTLP/HTTP JSON                             |
| Copilot CLI  | Yes   | Yes   | `copilot` wrapper function in the shell profile        | OTLP/HTTP protobuf (traces, metrics)       |
| Gemini CLI   | Yes   | Yes   | `~/.gemini/settings.json`                              | OTLP/HTTP JSON                             |
| Codex CLI    | Yes   | Yes   | `~/.codex/config.toml` `[otel]`                        | OTLP/HTTP protobuf (logs, metrics, traces) |
| Qwen Code    | Yes   | Yes   | `~/.qwen/settings.json` + `OTEL_EXPORTER_OTLP_HEADERS` | OTLP/HTTP JSON                             |
| Cursor       | Yes   | Yes   | Hook script + `~/.cursor/hooks.json`                   | OTLP/HTTP JSON logs                        |
| Windsurf     | Yes   | Yes   | Hook script + `~/.codeium/windsurf/hooks.json`         | OTLP/HTTP JSON logs                        |
| Antigravity  | Yes   | Yes   | Hook script + `~/.gemini/config/hooks.json`            | OTLP/HTTP JSON logs                        |
| Aider        | Yes   | Yes   | `~/.aider.conf.yml`                                    | —                                          |
| OpenCode     | Yes   | Yes   | `experimental.openTelemetry` + `opencode` wrapper      | OTLP/HTTP JSON (logs, traces)              |

Notes:

- **Shell profile.** `install` adds a marked block to your shell profile exporting `FLOWSTATE_OTLP_ENDPOINT`, `FLOWSTATE_OTLP_KEY`, `OTEL_EXPORTER_OTLP_HEADERS` (needed by Copilot Chat, Gemini CLI and Qwen Code, which read headers from the environment) and, when `FLOWSTATE_TEAM_ID` / `FLOWSTATE_EMAIL` are set, `OTEL_RESOURCE_ATTRIBUTES`. It never sets `service.name`: each tool reports its own. `OTEL_EXPORTER_OTLP_ENDPOINT` is never exported globally; Copilot CLI and OpenCode get it through small wrapper functions so other OpenTelemetry SDKs started from your shell keep their own endpoint. Those SDKs do still inherit `OTEL_EXPORTER_OTLP_HEADERS`, so a local app exporting to its own collector will send it your Flowstate key; set `OTEL_EXPORTER_OTLP_HEADERS` explicitly for such apps.
- **Hook tools (Cursor, Windsurf, Antigravity)** send one log record per hook with ids, the model, line counts, the file extension and prompt length only. Prompt text, file contents, paths, commands and transcripts are never sent. Editors opened from the Dock read the endpoint and key from `~/.config/flowstate-telemetry/env` (mode 0600). The scripts need `jq` and `curl`.
- **Apps opened from the Dock (macOS)** do not read your shell profile. VS Code 1.138+ takes the key from the `github.copilot.chat.otel.headers` setting; for older builds use `install --gui-env` (below) or start VS Code with `code` from a terminal.
- **OpenCode**: upstream marks `experimental.openTelemetry` as deprecated.
- **Prompt capture** is off unless you pass `--prompts` (Gemini CLI and Qwen Code default to logging prompts, so `logPrompts` is written as `false` explicitly).

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

### With prompt capture

```bash
flowstate-telemetry install --all --prompts
```

### Configure a single tool

```bash
flowstate-telemetry install --tool claude-code
```

### Expose the variables to Dock-launched apps (macOS)

```bash
flowstate-telemetry install --all --gui-env
```

Runs `launchctl setenv` for `FLOWSTATE_OTLP_ENDPOINT`, `FLOWSTATE_OTLP_KEY` and `OTEL_EXPORTER_OTLP_HEADERS`, and installs `~/Library/LaunchAgents/inc.flowstate.telemetry.env.plist` to repeat it at login. Every GUI app you run can then see these variables, so it is opt-in. Restart running apps afterwards. `remove --all` undoes it.

### Preview changes (dry run)

```bash
flowstate-telemetry install --all --dry-run
```

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
