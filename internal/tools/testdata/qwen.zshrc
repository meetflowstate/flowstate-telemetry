
# --- Flowstate Telemetry: qwen-code ---
# Scopes OpenTelemetry settings to Qwen Code only.
function qwen { OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE=delta OTEL_EXPORTER_OTLP_HEADERS="x-flowstate-key=${FLOWSTATE_OTLP_KEY:-}" command qwen "$@"; }
# --- End Flowstate Telemetry: qwen-code ---
