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

| Platform        | CLI             | Install Method   | Fleet (MDM)                     |
| --------------- | --------------- | ---------------- | ------------------------------- |
| macOS (ARM/x64) | Fully supported | Homebrew, binary | Jamf, Ansible, Puppet           |
| Linux (x64)     | Fully supported | Binary           | Ansible, Puppet                 |
| Windows (x64)   | Not supported   | —                | Intune script only (PowerShell) |

The CLI itself does not run on Windows. The only Windows coverage is the Intune PowerShell script from `generate-mdm --format intune`, which configures Claude Code and VS Code Copilot Chat for the logged-on user.

## Supported Tools

Every tool sends OpenTelemetry over plain HTTP (OTLP/HTTP, protobuf or JSON) to the Flowstate receiver at `https://otel.flowstate.host` (`/v1/logs`, `/v1/metrics`, `/v1/traces`), authenticated with an `x-flowstate-key` header. gRPC is never configured.

| Tool         | macOS | Linux | Config Method                                     | Transport                                  |
| ------------ | ----- | ----- | ------------------------------------------------- | ------------------------------------------ |
| Claude Code  | Yes   | Yes   | `~/.claude/settings.json` env block               | OTLP/HTTP protobuf, delta metrics          |
| Copilot Chat | Yes   | Yes   | VS Code `settings.json` (`…otel.headers`, 1.138+) | OTLP/HTTP JSON (`exporterType: otlp-http`) |
| Copilot CLI  | Yes   | Yes   | `copilot` wrapper function in the shell profile   | OTLP/HTTP protobuf (traces, delta metrics) |
| Gemini CLI   | Yes   | Yes   | `~/.gemini/settings.json` + `gemini` wrapper      | OTLP/HTTP JSON, delta metrics              |
| Codex CLI    | Yes   | Yes   | `~/.codex/config.toml` `[otel]`                   | OTLP/HTTP protobuf (logs, metrics, traces) |
| Qwen Code    | Yes   | Yes   | `~/.qwen/settings.json` + `qwen` wrapper          | OTLP/HTTP JSON, delta metrics              |
| Cursor       | Yes   | Yes   | Hook script + `~/.cursor/hooks.json`              | OTLP/HTTP JSON logs                        |
| Windsurf     | Yes   | Yes   | Hook script + `~/.codeium/windsurf/hooks.json`    | OTLP/HTTP JSON logs                        |
| Antigravity  | Yes   | Yes   | Hook script + `~/.gemini/config/hooks.json`       | OTLP/HTTP JSON logs                        |
| Aider        | Yes   | Yes   | — (no telemetry export; see below)                | —                                          |
| OpenCode     | Yes   | Yes   | `experimental.openTelemetry` + `opencode` wrapper | OTLP/HTTP JSON (logs, traces)              |

Notes:

- **Where the key goes.** The key is scoped to each tool and is never exported as `OTEL_EXPORTER_OTLP_HEADERS`: every OpenTelemetry SDK reads that variable, so any other instrumented program would send your Flowstate key to its own (third-party) collector, and it would clobber a value you set yourself. Each tool gets the key from its own settings where it has one (Claude Code `env` block, Codex `[otel]` headers, Gemini CLI `telemetry.otlpHeaders` on 0.65+, Copilot Chat `github.copilot.chat.otel.headers`). Tools that only read the environment (Copilot CLI, OpenCode, Qwen Code, Gemini CLI before 0.65) get small wrapper functions in your shell profile (`copilot`, `opencode`, `qwen`, `gemini`) that set the `OTEL_*` variables for that one command; the `gemini` and `qwen` wrappers also set delta metrics temporality. Only commands started from a shell that reads the profile are covered. Variables a tool passes on to programs it runs itself are outside this tool's control.
- **Shell profile.** `install` adds a marked block exporting only `FLOWSTATE_OTLP_ENDPOINT` and `FLOWSTATE_OTLP_KEY` (read by the wrappers and hook scripts; no OpenTelemetry SDK reads them). No generic `OTEL_*` variable is exported. The key itself is not in the profile (which is usually readable by other accounts): it is stored in `~/.config/flowstate-telemetry/env` (mode 0600), and the block reads `FLOWSTATE_OTLP_KEY` from that file when the shell starts (parsed, never sourced). Re-installing moves a key an older release wrote into the profile, and `verify` warns while the profile still holds it. Removing the block leaves your surrounding lines exactly as they were.
- **File permissions.** These files hold the key: `~/.config/flowstate-telemetry/env`, `~/.claude/settings.json`, VS Code's `settings.json`, `~/.codex/config.toml`, `~/.gemini/settings.json` (Gemini CLI 0.65+, when the key goes into `telemetry.otlpHeaders`) and, with `--gui-env`, `~/Library/LaunchAgents/inc.flowstate.telemetry.env.plist`. The env file and the plist are always set to 0600. The others are created 0600; when `install` writes the key into an existing one that group or others can read, it removes those permissions (0644 becomes 0600). Modes are only ever tightened, never loosened (0400 stays 0400). A settings file or shell profile that is a symlink (a dotfiles repository) is written through, so it stays a symlink.
- **Team and e-mail attributes.** When `FLOWSTATE_TEAM_ID` / `FLOWSTATE_EMAIL` are set, `flowstate.team_id` / `flowstate.email` go into `OTEL_RESOURCE_ATTRIBUTES` (values percent-encoded as the OpenTelemetry spec requires, so `,`, `=`, `%`, spaces and non-ASCII characters are safe) scoped like the key: Claude Code's `env` block (merged with any attributes you set there yourself; `remove` takes out only the `flowstate.*` ones) and the `copilot`, `opencode`, `gemini` and `qwen` wrappers. They are never exported globally, where every OpenTelemetry program would send them to its own collector. Tools with no scoped option (Codex, Copilot Chat, hook tools) do not get them; the receiver identifies the user from the key, so nothing is lost. `service.name` is never set: each tool reports its own. `install` removes the copy older releases exported from the shell profile, and `verify` fails while it is still exported anywhere (environment, profile, LaunchAgent, machine-wide files).
- **Copilot Chat** takes the key only from the `github.copilot.chat.otel.headers` setting. VS Code builds without that setting (before 1.138) cannot authenticate: update VS Code. **Settings Sync** would upload that setting, and so your key, to your sync account and every synced machine; `install` adds it to `settingsSync.ignoredSettings` and `verify` warns when it is missing. If Settings Sync was on before, rotate the key.
- **Hook tools (Cursor, Windsurf, Antigravity)** send one log record per hook with ids, the model, line counts and the file extension (`code.file_extension`, lower case without the leading dot, e.g. `tsx`). Cursor and Windsurf also send the prompt length (`prompt.length`); Antigravity sends no prompt length but adds the tool name (`gen_ai.tool.name`). Prompt text, file contents, paths, commands and transcripts are never sent. Editors opened from the Dock read the endpoint and key from `~/.config/flowstate-telemetry/env` (mode 0600). The key header is passed to `curl` on stdin, never on its command line (visible in `ps`). The scripts need `jq` and `curl`.
- **OpenCode**: upstream marks `experimental.openTelemetry` as deprecated. The `opencode` wrapper also sets `OTEL_EXPORTER_OTLP_PROTOCOL=http/json`.
- **Aider** has no telemetry export, so nothing is sent to Flowstate and there is nothing to configure. Earlier releases set `llm-history-file` in `~/.aider.conf.yml`, which makes Aider write every prompt and response to a local file; `install` (and `remove`) take that setting out again, and `verify` fails while it is there. A history file you set yourself is left alone (`verify` warns about it).
- **Prompt and response logging stays off.** Flowstate drops content anyway, so it should not leave the machine. Gemini CLI and Qwen Code default to logging prompts, so `logPrompts` is written as `false` explicitly. `--prompts` exists but is not recommended, and `verify` warns when a tool logs prompts.
- **Codex** exports OTLP metrics only while `[analytics] enabled` is `true` (the default); `verify` warns when it is `false`.

### Upgrading from otel.flowstate.inc

Earlier releases used the retired gRPC receiver `https://otel.flowstate.inc` and exported it as `FLOWSTATE_OTLP_ENDPOINT` from the shell profile (plus a global `OTEL_EXPORTER_OTLP_HEADERS`). Any `otel.flowstate.inc` endpoint, with or without a port or path, is treated as unset, so re-running `install --all` configures every tool for `https://otel.flowstate.host` and rewrites the shell block, the hook env file and an existing `--gui-env` LaunchAgent. The hook scripts ignore the old endpoint too. `verify` fails while the old endpoint remains anywhere (environment, shell profile, tool configs, hook env file, LaunchAgent, machine-wide files left by older MDM templates), and while a global `OTEL_EXPORTER_OTLP_HEADERS` carrying the key is left over anywhere. Open a new terminal after installing.

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

# Check the local configuration
flowstate-telemetry verify
```

## Usage

### Configure all detected tools

```bash
flowstate-telemetry install --all
```

### Check the configuration

```bash
flowstate-telemetry verify
```

`verify` sends nothing: it checks this machine's local configuration. That is the environment (a global `OTEL_EXPORTER_OTLP_HEADERS` or `OTEL_RESOURCE_ATTRIBUTES` left by an older release, the retired endpoint, the shell profile, the `--gui-env` LaunchAgent, machine-wide files left by older MDM templates) and, for each detected tool, its settings files and wrapper functions. It exits 1 when any check fails **or has a warning** (for example prompt logging switched on, or VS Code Settings Sync able to upload the key), so it can gate a script.

### Configure a single tool

```bash
flowstate-telemetry install --tool claude-code
```

### Expose the variables to Dock-launched apps (macOS)

```bash
flowstate-telemetry install --all --gui-env
```

Runs `launchctl setenv` for `FLOWSTATE_OTLP_ENDPOINT` and `FLOWSTATE_OTLP_KEY`, and installs `~/Library/LaunchAgents/inc.flowstate.telemetry.env.plist` (mode 0600) to repeat it at login. It is rarely needed (the hook scripts read `~/.config/flowstate-telemetry/env`), and it is opt-in because it exposes the key more widely than anything else here:

- every GUI app you run can read `FLOWSTATE_OTLP_KEY` from its environment;
- `launchctl` has no way to take a value except as an argument, so the key is on a command line: `launchctl setenv FLOWSTATE_OTLP_KEY <key>` when you install, and the LaunchAgent's `/bin/sh -c '… launchctl setenv FLOWSTATE_OTLP_KEY <key>'` at every login. While those run, other processes on the machine can read the key with `ps`.

Prefer per-tool configuration (plain `install`, which keeps the key in 0600 files and passes it on stdin) and use `--gui-env` only when an editor launched from the Dock must see the variables. It never publishes `OTEL_EXPORTER_OTLP_HEADERS`, and unsets the copy earlier releases published (only when it carries `x-flowstate-key`: a value you set for your own collector is left alone). Restart running apps afterwards. `remove --all` undoes it.

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

- **One key per artefact.** The artefact embeds the key you generate it with, so every user it is deployed to is attributed to that key's owner. Per-user attribution needs per-user keys: substitute a per-user key at deployment (Jamf: script parameter 4 overrides the embedded key; Ansible: `flowstate_key` per host; Puppet: `$key` from Hiera; Intune: `$Key`). Jamf passes script parameters to the script as command-line arguments (that is how Jamf runs scripts), so a parameter-4 key is briefly visible to `ps` on the Mac while the policy runs; the embedded key is not.
- **Validated values.** `generate-mdm` requires an `https` endpoint with no quotes, spaces or shell characters and a key, team id and e-mail without control characters. The artefacts check values substituted at deployment the same way (Jamf parameter 4, Ansible `flowstate_endpoint` / `flowstate_key`, Puppet `$endpoint` / `$key`) and quote the key for each language they embed it in (PowerShell, YAML, Puppet, shell, JSON).
- **No machine-wide key.** The templates write the key only into each user's tool settings and per-user files or variables (Jamf, Ansible, Puppet: `~/.config/flowstate-telemetry/env`, mode 0600, owned by the user; Intune: user-scope `FLOWSTATE_*` variables, so run it as the logged-on user). They no longer write `/etc/flowstate-telemetry.env`, `/etc/profile.d/flowstate-telemetry.sh` or `/etc/environment` entries, never set `OTEL_EXPORTER_OTLP_HEADERS` machine-, session- or shell-wide, and remove what earlier versions wrote there (Jamf also removes the old LaunchAgent and unsets the variables in the logged-in user's session). Ansible and Puppet add a profile block to `~/.bashrc`, and to `~/.zshrc` on macOS, that reads the key from the env file (the profile never stores it) and defines the `gemini` wrapper function. Settings files holding the key are created 0600.
- **Written as the user.** Jamf and Puppet run as root, but write every file under a home directory as that user (Jamf: `sudo -u`; Puppet: a script run by an `exec` with `user =>`), so a symlink a user plants in their home cannot redirect a root write. Puppet has no root-owned `file` resources under home directories: the user-run script writes the env file, the profile block and the Claude Code, VS Code (`~/Library/Application Support/Code/User` on macOS, `~/.config/Code/User` on Linux) and Gemini CLI `settings.json` files, merging into what is there (your other settings are kept; a file that is not plain JSON, such as VS Code settings with comments, is left alone with a warning). A symlinked dotfile (`~/.zshrc`, `~/.bashrc`, `settings.json`) is followed and stays a symlink, but only when its target is a file the user owns in a directory the user owns. Puppet's profile check compares content, so lines you add after the block do not trigger a rewrite. The key is passed on stdin or in the environment, never on a command line (visible in `ps`). Both need a `python3` that runs without prompting (the Command Line Tools or Homebrew on macOS): without one, Jamf creates a missing `~/.claude/settings.json` itself (JSON-escaping the key) and warns about an existing one, and Puppet skips the per-user configuration with a warning on each run.
- **Intune machine-wide clean-up.** The Intune script runs as the logged-on user, who cannot remove the machine-wide `FLOWSTATE_OTLP_KEY`, `OTEL_EXPORTER_OTLP_HEADERS` and `OTEL_RESOURCE_ATTRIBUTES` that earlier versions (run as SYSTEM) set. It tries, warns, and carries on configuring the user's tools. To remove them, deploy the separate clean-up script once with "Run this script using the logged on credentials: No" (SYSTEM); it embeds no key and removes only values Flowstate wrote:

  ```bash
  flowstate-telemetry generate-mdm --format intune --machine-cleanup
  ```

## Global flags and commands

| Flag / command          | Description                                                                                                                                                                                                                                    |
| ----------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `--key <key>`           | Flowstate API key (overrides `FLOWSTATE_OTLP_KEY`). Note that a key on the command line is visible to `ps`; prefer the variable.                                                                                                               |
| `--endpoint <url>`      | OTLP/HTTP receiver base URL (overrides `FLOWSTATE_OTLP_ENDPOINT`; default `https://otel.flowstate.host`). Must be `https` (`install` also accepts `http://localhost`).                                                                         |
| `--verbose`             | Print more detail (for example tools skipped because they are not detected).                                                                                                                                                                   |
| `version` / `--version` | Print the version, commit, build date, Go version and platform.                                                                                                                                                                                |
| `proxy`                 | Deprecated. The receiver no longer accepts proxy events and `proxy install` is disabled; `sudo flowstate-telemetry proxy uninstall` removes an earlier install (until then its daemon only tunnels traffic, intercepting and sending nothing). |

`remove --all` cleans every tool's files whether or not the tool is still installed, so no key is left behind in, say, `~/.claude/settings.json` after `claude` itself was uninstalled. A file it cannot parse (say, a broken `~/.gemini/settings.json`) for a tool that is not configured is left alone with a warning, not counted as a failure; for a configured tool, or one named with `--tool`, it still fails.

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
