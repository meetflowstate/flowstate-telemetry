# --- Flowstate Telemetry: opencode ---
# Scopes OpenTelemetry settings to OpenCode only.
function opencode { OTEL_EXPORTER_OTLP_ENDPOINT="${FLOWSTATE_OTLP_ENDPOINT:-https://otel.flowstate.host}" OTEL_EXPORTER_OTLP_PROTOCOL=http/json OTEL_EXPORTER_OTLP_HEADERS="x-flowstate-key=${FLOWSTATE_OTLP_KEY:-}" command opencode "$@"; }
# --- End Flowstate Telemetry: opencode ---
