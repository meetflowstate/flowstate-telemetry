#!/usr/bin/env bash
# Flowstate telemetry hook for Google Antigravity (installed by flowstate-telemetry).
#
# Usage (from ~/.gemini/config/hooks.json): flowstate-antigravity.sh <Event>,
# hook JSON on stdin. Antigravity does not put the event name in the payload,
# so it is passed as the first argument. Posts one OTLP/HTTP JSON log record
# to <endpoint>/v1/logs carrying ids, the model, the tool name, line counts
# and file extensions. It never sends tool arguments, file contents, paths,
# commands or the raw payload. It always prints {} and exits 0.

TOOL=antigravity
INPUT=$(cat 2>/dev/null || true)
HOOK="${1:-}"

printf '{}\n'

command -v jq >/dev/null 2>&1 || exit 0
command -v curl >/dev/null 2>&1 || exit 0
[ -n "$HOOK" ] || exit 0

CONF="${XDG_CONFIG_HOME:-$HOME/.config}/flowstate-telemetry/env"
conf() { [ -r "$CONF" ] && sed -n "s/^$1=//p" "$CONF" 2>/dev/null | head -n 1; return 0; }
ENDPOINT="${FLOWSTATE_OTLP_ENDPOINT:-$(conf FLOWSTATE_OTLP_ENDPOINT)}"
ENDPOINT="${ENDPOINT:-https://otel.flowstate.host}"
ENDPOINT="${ENDPOINT%/}"
case "$ENDPOINT" in */v1/logs) ;; *) ENDPOINT="$ENDPOINT/v1/logs" ;; esac
KEY="${FLOWSTATE_OTLP_KEY:-$(conf FLOWSTATE_OTLP_KEY)}"

# PostToolUse also fires for non-tool steps (toolCall null); those are skipped.
# Edit tool arguments are PascalCase (write_to_file, replace_file_content,
# multi_replace_file_content); the chunk schema is undocumented, so every
# field is read defensively and missing ones count as zero lines.
PAYLOAD=$(printf '%s' "$INPUT" | jq -c --arg tool "$TOOL" --arg hook "$HOOK" '
  def str($k; $v): {key: $k, value: {stringValue: $v}};
  def int($k; $v): {key: $k, value: {intValue: ($v | tostring)}};
  def nlines: if type == "string" and length > 0
    then (split("\n") | length) - (if endswith("\n") then 1 else 0 end) else 0 end;
  def ext: if type == "string"
    then ((split("/") | last) | (capture("^.+\\.(?<e>[A-Za-z0-9_+-]{1,16})$").e // "") | ascii_downcase)
    else "" end;
  def nonempty: type == "string" and length > 0;
  def obj: if type == "object" then . else {} end;
  def pick($a; $b): (.[$a] // .[$b]);

  (. | obj) as $in
  | ($in.toolCall | obj) as $call
  | ($call.args | obj) as $args
  | ($call.name // "") as $toolName
  | if $hook == "PostToolUse" and ($toolName | nonempty | not) then empty else
    ($in.conversationId // $in.conversation_id // "") as $conv
  | ($in.modelName // $in.model_name // "") as $model
  | ($args | pick("TargetFile"; "target_file") // pick("file_path"; "FilePath")) as $file
  | (if $toolName == "write_to_file" then
        {added: ($args | pick("CodeContent"; "code_content") | nlines), removed: 0}
      elif $toolName == "replace_file_content" then
        {added: ($args | pick("ReplacementContent"; "replacement_content") | nlines),
         removed: ($args | pick("TargetContent"; "target_content") | nlines)}
      elif $toolName == "multi_replace_file_content" then
        ([$args | pick("ReplacementChunks"; "replacement_chunks") | if type == "array" then .[] else empty end | obj]) as $chunks
        | {added: ([$chunks[] | pick("ReplacementContent"; "replacement_content") | nlines] | add // 0),
           removed: ([$chunks[] | pick("TargetContent"; "target_content") | nlines] | add // 0)}
      else null end) as $counts
  | (if $counts != null
      then [int("code.lines_added"; $counts.added), int("code.lines_removed"; $counts.removed)]
           + ([$file | ext] | map(select(nonempty)) | map(str("code.file_extension"; .)))
      else [] end) as $edit
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
            + (if ($toolName | nonempty) then [str("gen_ai.tool.name"; $toolName)] else [] end)
            + $edit)
        }]
      }]
    }]} end' 2>/dev/null) || exit 0
[ -n "$PAYLOAD" ] || exit 0

curl -sf --max-time 3 -X POST "$ENDPOINT" \
  -H "Content-Type: application/json" \
  ${KEY:+-H "x-flowstate-key: $KEY"} \
  -d "$PAYLOAD" >/dev/null 2>&1 &

exit 0
