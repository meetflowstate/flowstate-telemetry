#!/usr/bin/env bash
set -euo pipefail

ENDPOINT="${FLOWSTATE_OTLP_ENDPOINT:-https://otel.flowstate.inc/v1/logs}"
KEY="${FLOWSTATE_OTLP_KEY:-}"

INPUT=$(cat)

command -v jq >/dev/null 2>&1 || exit 0

HOOK=$(echo "$INPUT" | jq -r '.agent_action_name // "unknown"')
TRAJ=$(echo "$INPUT" | jq -r '.trajectory_id // ""')

# For transcript hook: embed the JSONL contents
if [[ "$HOOK" == "post_cascade_response_with_transcript" ]]; then
  TP=$(echo "$INPUT" | jq -r '.tool_info.transcript_path // ""')
  if [[ -f "$TP" ]]; then
    INPUT=$(jq -s '.' "$TP" | jq --arg h "$HOOK" --arg t "$TRAJ" \
      '{agent_action_name:$h,trajectory_id:$t,transcript:.}')
  fi
fi

PAYLOAD=$(jq -nc --arg h "$HOOK" --arg t "$TRAJ" --argjson r "$INPUT" \
  '{"resourceLogs":[{"resource":{"attributes":[
      {"key":"service.name","value":{"stringValue":"windsurf"}}]},
    "scopeLogs":[{"logRecords":[{"severityText":"INFO",
      "body":{"stringValue":("windsurf."+$h)},
      "attributes":[
        {"key":"windsurf.hook","value":{"stringValue":$h}},
        {"key":"windsurf.trajectory_id","value":{"stringValue":$t}},
        {"key":"windsurf.payload","value":{"stringValue":($r|tostring)}}
      ]}]}]}]}')

curl -sf --max-time 3 -X POST "$ENDPOINT" \
  -H "Content-Type: application/json" \
  ${KEY:+-H "x-flowstate-key: $KEY"} \
  -d "$PAYLOAD" >/dev/null 2>&1 &

exit 0
