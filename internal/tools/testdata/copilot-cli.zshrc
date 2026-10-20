
# --- Flowstate Telemetry: copilot-cli ---
# Scopes OpenTelemetry settings to the Copilot CLI only.
function copilot { COPILOT_OTEL_ENABLED=true OTEL_EXPORTER_OTLP_ENDPOINT="${FLOWSTATE_OTLP_ENDPOINT:-https://otel.flowstate.host}" OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf OTEL_EXPORTER_OTLP_HEADERS="x-flowstate-key=${FLOWSTATE_OTLP_KEY:-}" OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE=delta command copilot "$@"; }
# --- End Flowstate Telemetry: copilot-cli ---
