# --- Flowstate Telemetry: copilot-cli ---
# Scopes OpenTelemetry settings to the Copilot CLI only.
function copilot { COPILOT_OTEL_ENABLED=true OTEL_EXPORTER_OTLP_ENDPOINT="${FLOWSTATE_OTLP_ENDPOINT:-https://otel.flowstate.host}" OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf OTEL_EXPORTER_OTLP_HEADERS="x-flowstate-key=${FLOWSTATE_OTLP_KEY:-}" OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE=delta OTEL_RESOURCE_ATTRIBUTES='flowstate.team_id=team-1,flowstate.email=dev@example.com' command copilot "$@"; }
# --- End Flowstate Telemetry: copilot-cli ---

# --- Flowstate Telemetry: opencode ---
# Scopes OpenTelemetry settings to OpenCode only.
function opencode { OTEL_EXPORTER_OTLP_ENDPOINT="${FLOWSTATE_OTLP_ENDPOINT:-https://otel.flowstate.host}" OTEL_EXPORTER_OTLP_PROTOCOL=http/json OTEL_EXPORTER_OTLP_HEADERS="x-flowstate-key=${FLOWSTATE_OTLP_KEY:-}" OTEL_RESOURCE_ATTRIBUTES='flowstate.team_id=team-1,flowstate.email=dev@example.com' command opencode "$@"; }
# --- End Flowstate Telemetry: opencode ---

# --- Flowstate Telemetry: gemini-cli ---
# Scopes OpenTelemetry settings to Gemini CLI only.
function gemini { OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE=delta OTEL_RESOURCE_ATTRIBUTES='flowstate.team_id=team-1,flowstate.email=dev@example.com' command gemini "$@"; }
# --- End Flowstate Telemetry: gemini-cli ---

# --- Flowstate Telemetry: qwen-code ---
# Scopes OpenTelemetry settings to Qwen Code only.
function qwen { OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE=delta OTEL_EXPORTER_OTLP_HEADERS="x-flowstate-key=${FLOWSTATE_OTLP_KEY:-}" OTEL_RESOURCE_ATTRIBUTES='flowstate.team_id=team-1,flowstate.email=dev@example.com' command qwen "$@"; }
# --- End Flowstate Telemetry: qwen-code ---

# --- Flowstate Telemetry ---
export FLOWSTATE_OTLP_ENDPOINT='https://otel.flowstate.host'
# FLOWSTATE_OTLP_KEY is read from the env file (mode 0600), not stored here.
_fs_env='$HOME/.config/flowstate-telemetry/env'; if [ -r "$_fs_env" ]; then while IFS= read -r _fs_l || [ -n "$_fs_l" ]; do case "$_fs_l" in FLOWSTATE_OTLP_KEY=*) export FLOWSTATE_OTLP_KEY="${_fs_l#FLOWSTATE_OTLP_KEY=}"; break ;; esac; done < "$_fs_env"; fi; unset _fs_env _fs_l
# --- End Flowstate Telemetry ---
