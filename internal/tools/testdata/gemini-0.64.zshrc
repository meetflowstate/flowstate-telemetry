# --- Flowstate Telemetry: gemini-cli ---
# Scopes OpenTelemetry settings to Gemini CLI only.
function gemini { OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE=delta OTEL_EXPORTER_OTLP_HEADERS="x-flowstate-key=${FLOWSTATE_OTLP_KEY:-}" command gemini "$@"; }
# --- End Flowstate Telemetry: gemini-cli ---
