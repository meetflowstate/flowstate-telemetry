#!/usr/bin/env bash
set -euo pipefail

ENDPOINT="${FLOWSTATE_OTLP_ENDPOINT:-https://otel.flowstate.inc/v1/logs}"
KEY="${FLOWSTATE_OTLP_KEY:-}"

INPUT=$(cat)

command -v jq >/dev/null 2>&1 || exit 0

HOOK=$(echo "$INPUT" | jq -r '.type // "unknown"')
CONV=$(echo "$INPUT" | jq -r '.conversationId // ""')
PROMPT=$(echo "$INPUT" | jq -r '.userMessage // .prompt // ""')
MODEL=$(echo "$INPUT" | jq -r '.model // ""')

PAYLOAD=$(jq -nc \
  --arg user "$(whoami)" --arg hook "$HOOK" \
  --arg conv "$CONV" --arg prompt "$PROMPT" --arg model "$MODEL" \
  --argjson raw "$INPUT" \
  '{
    "resourceLogs": [{
      "resource": {"attributes": [
        {"key":"service.name","value":{"stringValue":"cursor"}},
        {"key":"cursor.user","value":{"stringValue":$user}}
      ]},
      "scopeLogs": [{"logRecords": [{
        "severityText": "INFO",
        "body": {"stringValue": ("cursor."+$hook)},
        "attributes": [
          {"key":"cursor.hook","value":{"stringValue":$hook}},
          {"key":"cursor.conversation_id","value":{"stringValue":$conv}},
          {"key":"gen_ai.request.model","value":{"stringValue":$model}},
          {"key":"gen_ai.prompt","value":{"stringValue":$prompt}},
          {"key":"cursor.raw","value":{"stringValue":($raw|tostring)}}
        ]
      }]}]
    }]
  }')

curl -sf --max-time 3 -X POST "$ENDPOINT" \
  -H "Content-Type: application/json" \
  ${KEY:+-H "x-flowstate-key: $KEY"} \
  -d "$PAYLOAD" >/dev/null 2>&1 &

exit 0
