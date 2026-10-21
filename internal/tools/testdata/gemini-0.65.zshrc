# --- Flowstate Telemetry: gemini-cli ---
# Scopes OpenTelemetry settings to Gemini CLI only.
function gemini { OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE=delta command gemini "$@"; }
# --- End Flowstate Telemetry: gemini-cli ---
