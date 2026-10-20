#!/usr/bin/env bash
# Flowstate telemetry hook for Cursor (installed by flowstate-telemetry).
#
# Usage (from ~/.cursor/hooks.json): flowstate.sh <hookName>, hook JSON on stdin.
# Posts one OTLP/HTTP JSON log record to <endpoint>/v1/logs carrying ids, the
# model, line counts, file extensions (no leading dot) and prompt length. It never sends prompt
# text, file contents, paths, commands or the raw payload. It always exits 0
# and never delays the editor.

TOOL=cursor
INPUT=$(cat 2>/dev/null || true)
HOOK="${1:-}"

# beforeSubmitPrompt must answer {"continue": true}; the others accept {}.
if [ "$HOOK" = "beforeSubmitPrompt" ]; then
  printf '{"continue":true}\n'
else
  printf '{}\n'
fi

command -v jq >/dev/null 2>&1 || exit 0
command -v curl >/dev/null 2>&1 || exit 0

if [ -z "$HOOK" ]; then
  HOOK=$(printf '%s' "$INPUT" | jq -r '.hook_event_name // empty' 2>/dev/null || true)
fi
[ -n "$HOOK" ] || exit 0

# Editors started from the Dock never read the shell profile, so fall back to
# the env file written by `flowstate-telemetry install` (parsed, not sourced).
CONF="${XDG_CONFIG_HOME:-$HOME/.config}/flowstate-telemetry/env"
conf() { [ -r "$CONF" ] && sed -n "s/^$1=//p" "$CONF" 2>/dev/null | head -n 1; return 0; }
# The retired otel.flowstate.inc receiver (exported by older releases) counts
# as unset.
legacy() { case "$1" in *otel.flowstate.inc|*otel.flowstate.inc[:/]*) return 0 ;; esac; return 1; }
ENDPOINT="${FLOWSTATE_OTLP_ENDPOINT:-}"
legacy "$ENDPOINT" && ENDPOINT=""
[ -n "$ENDPOINT" ] || ENDPOINT="$(conf FLOWSTATE_OTLP_ENDPOINT)"
legacy "$ENDPOINT" && ENDPOINT=""
ENDPOINT="${ENDPOINT:-https://otel.flowstate.host}"
ENDPOINT="${ENDPOINT%/}"
case "$ENDPOINT" in */v1/logs) ;; *) ENDPOINT="$ENDPOINT/v1/logs" ;; esac
KEY="${FLOWSTATE_OTLP_KEY:-$(conf FLOWSTATE_OTLP_KEY)}"

PAYLOAD=$(printf '%s' "$INPUT" | jq -c --arg tool "$TOOL" --arg hook "$HOOK" '
  def str($k; $v): {key: $k, value: {stringValue: $v}};
  def int($k; $v): {key: $k, value: {intValue: ($v | tostring)}};
  def nlines: if type == "string" and length > 0
    then (split("\n") | length) - (if endswith("\n") then 1 else 0 end) else 0 end;
  def ext: if type == "string"
    then ((split("/") | last) | (capture("^.+\\.(?<e>[A-Za-z0-9_+-]{1,16})$").e // "") | ascii_downcase)
    else "" end;
  def nonempty: type == "string" and length > 0;

  (if type == "object" then . else {} end) as $in
  | ($in.conversation_id // $in.session_id // "") as $conv
  | ($in.model // "") as $model
  | (if ($hook == "afterFileEdit" or $hook == "afterTabFileEdit") and ($in.edits | type) == "array"
      then [int("code.lines_added"; ([$in.edits[] | .new_string | nlines] | add // 0)),
            int("code.lines_removed"; ([$in.edits[] | .old_string | nlines] | add // 0))]
           + ([$in.file_path | ext] | map(select(nonempty)) | map(str("code.file_extension"; .)))
      else [] end) as $edit
  | (if $hook == "beforeSubmitPrompt" and ($in.prompt | type) == "string"
      then [int("prompt.length"; ($in.prompt | length))] else [] end) as $prompt
  | ($tool + ".hook." + $hook) as $event
  | {resourceLogs: [{
      resource: {attributes: [str("service.name"; $tool)]},
      scopeLogs: [{
        scope: {name: "flowstate-telemetry.hooks"},
        logRecords: [{
          timeUnixNano: (((now * 1000) | floor | tostring) + "000000"),
          severityNumber: 9,
          severityText: "INFO",
          body: {stringValue: $event},
          attributes: (
            [str("event.name"; $event)]
            + (if ($conv | nonempty) then [str($tool + ".conversation_id"; $conv)] else [] end)
            + (if ($model | nonempty) then [str("gen_ai.request.model"; $model)] else [] end)
            + $edit + $prompt)
        }]
      }]
    }]}' 2>/dev/null) || exit 0
[ -n "$PAYLOAD" ] || exit 0

# The key header goes to curl on stdin (-H @-), never on its command line,
# which any local user can read with ps.
send() {
  if [ -n "$KEY" ]; then
    printf 'x-flowstate-key: %s\n' "$KEY" |
      curl -sf --max-time 3 -X POST "$ENDPOINT" -H "Content-Type: application/json" -H @- -d "$PAYLOAD"
  else
    curl -sf --max-time 3 -X POST "$ENDPOINT" -H "Content-Type: application/json" -d "$PAYLOAD" </dev/null
  fi
}
send >/dev/null 2>&1 &

exit 0
