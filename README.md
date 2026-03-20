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

| Platform        | Status             | Install Method        |
| --------------- | ------------------ | --------------------- |
| macOS (ARM/x64) | Fully supported    | Homebrew, binary      |
| Linux (x64)     | Fully supported    | Binary                |
| Windows (x64)   | Not yet supported  | —                     |

## Supported Tools

| Tool         | macOS | Linux | Config Method                       |
| ------------ | ----- | ----- | ----------------------------------- |
| Claude Code  | Yes   | Yes   | `~/.claude/settings.json` env block |
| Copilot Chat | Yes   | Yes   | VS Code `settings.json`             |
| Gemini CLI   | Yes   | Yes   | `~/.gemini/settings.json`           |
| Codex CLI    | Yes   | Yes   | `~/.codex/config.toml`              |
| Qwen Code    | Yes   | Yes   | `~/.qwen/settings.json`             |
| Cursor       | Yes   | Yes   | Hook scripts + `hooks.json`         |
| Windsurf     | Yes   | Yes   | Hook scripts + `hooks.json`         |
| Aider        | Yes   | Yes   | `~/.aider.conf.yml`                 |
| OpenCode     | Yes   | Yes   | `~/.config/opencode/opencode.json`  |

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

| Variable                  | Description                                                     |
| ------------------------- | --------------------------------------------------------------- |
| `FLOWSTATE_OTLP_ENDPOINT` | OTel collector endpoint (default: `https://otel.flowstate.inc`) |
| `FLOWSTATE_OTLP_KEY`      | Flowstate API key for authentication                            |
| `FLOWSTATE_TEAM_ID`       | Team identifier for resource attributes                         |
| `FLOWSTATE_EMAIL`         | User email for resource attributes                              |

## Learn More

- [AI Cost Attribution Platform](https://www.flowstate.inc/platform/ai-cost-attribution/)
- [Flowstate Documentation](https://www.flowstate.inc)

## License

MIT
