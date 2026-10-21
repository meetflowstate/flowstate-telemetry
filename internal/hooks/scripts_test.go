package hooks

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/meetflowstate/flowstate-telemetry/internal/config"
)

// The hook scripts are exercised for real: bash runs them with sample stdin,
// jq builds the payload, and a stub curl on PATH records its arguments and
// its stdin (where the key header goes).
// Every sample carries SECRET markers in prompt text, file contents, paths
// and commands; none may reach the request.

type hookCase struct {
	name    string
	script  string
	hook    string
	stdin   string
	tool    string
	stdout  string
	noSend  bool
	want    map[string]string // attribute key -> value (string or int as text)
	without []string          // attribute keys that must be absent
}

var hookCases = []hookCase{
	{
		name: "cursor afterFileEdit", script: CursorHookScript, hook: "afterFileEdit", tool: "cursor", stdout: "{}",
		stdin: `{"hook_event_name":"afterFileEdit","conversation_id":"conv-1","generation_id":"g","model":"claude-4-sonnet",
			"user_email":"SECRET@example.com","workspace_roots":["/SECRET/root"],"file_path":"/SECRET/path/App.TSX",
			"edits":[{"old_string":"SECRET old\nline two\n","new_string":"SECRET new\nline two\nline three"}]}`,
		want: map[string]string{
			"cursor.conversation_id": "conv-1", "gen_ai.request.model": "claude-4-sonnet",
			"code.lines_added": "3", "code.lines_removed": "2", "code.file_extension": "tsx",
		},
		without: []string{"prompt.length"},
	},
	{
		name: "cursor afterTabFileEdit", script: CursorHookScript, hook: "afterTabFileEdit", tool: "cursor", stdout: "{}",
		stdin: `{"conversation_id":"conv-1","file_path":"/SECRET/Makefile",
			"edits":[{"old_string":"","new_string":"SECRET","range":{"start_line_number":1},"old_line":"SECRET","new_line":"SECRET"}]}`,
		want:    map[string]string{"code.lines_added": "1", "code.lines_removed": "0"},
		without: []string{"code.file_extension"},
	},
	{
		name: "cursor beforeSubmitPrompt", script: CursorHookScript, hook: "beforeSubmitPrompt", tool: "cursor",
		stdout: `{"continue":true}`,
		stdin:  `{"conversation_id":"conv-2","model":"gpt-5","prompt":"SECRET prompt ü","attachments":[{"type":"file","file_path":"/SECRET/a.go"}]}`,
		want:   map[string]string{"prompt.length": "15", "cursor.conversation_id": "conv-2"},
	},
	{
		name: "cursor stop", script: CursorHookScript, hook: "stop", tool: "cursor", stdout: "{}",
		stdin:   `{"conversation_id":"conv-3","status":"completed","loop_count":0}`,
		want:    map[string]string{"cursor.conversation_id": "conv-3"},
		without: []string{"code.lines_added", "prompt.length", "gen_ai.request.model"},
	},
	{
		name: "cursor sessionStart without argv", script: CursorHookScript, hook: "", tool: "cursor", stdout: "{}",
		stdin: `{"hook_event_name":"sessionStart","session_id":"sess-1","composer_mode":"agent"}`,
		want:  map[string]string{"event.name": "cursor.hook.sessionStart", "cursor.conversation_id": "sess-1"},
	},
	{
		name: "windsurf post_write_code", script: WindsurfHookScript, hook: "post_write_code", tool: "windsurf",
		stdin: `{"agent_action_name":"post_write_code","trajectory_id":"traj-1","execution_id":"e","model_name":"SWE-1.5",
			"tool_info":{"file_path":"/SECRET/path/main.py","edits":[{"old_string":"SECRET","new_string":"SECRET\nSECRET\n"}]}}`,
		want: map[string]string{
			"windsurf.conversation_id": "traj-1", "gen_ai.request.model": "SWE-1.5",
			"code.lines_added": "2", "code.lines_removed": "1", "code.file_extension": "py",
		},
	},
	{
		name: "windsurf pre_user_prompt", script: WindsurfHookScript, hook: "pre_user_prompt", tool: "windsurf",
		stdin:   `{"agent_action_name":"pre_user_prompt","trajectory_id":"traj-2","model_name":"Unknown","tool_info":{"user_prompt":"SECRET"}}`,
		want:    map[string]string{"prompt.length": "6"},
		without: []string{"gen_ai.request.model"},
	},
	{
		name: "windsurf post_cascade_response", script: WindsurfHookScript, hook: "post_cascade_response", tool: "windsurf",
		stdin: `{"agent_action_name":"post_cascade_response","trajectory_id":"traj-3","tool_info":{"response":"SECRET markdown"}}`,
		want:  map[string]string{"windsurf.conversation_id": "traj-3"},
	},
	{
		name: "antigravity replace_file_content", script: AntigravityHookScript, hook: "PostToolUse", tool: "antigravity", stdout: "{}",
		stdin: `{"conversationId":"ag-1","modelName":"gemini-pro-agent","workspacePaths":["/SECRET"],"transcriptPath":"/SECRET/t.jsonl",
			"stepIdx":3,"error":"","toolCall":{"name":"replace_file_content","args":{"TargetFile":"/SECRET/src/lib.rs",
			"TargetContent":"SECRET a\nSECRET b","ReplacementContent":"SECRET c","StartLine":1,"EndLine":2}}}`,
		want: map[string]string{
			"antigravity.conversation_id": "ag-1", "gen_ai.request.model": "gemini-pro-agent", "gen_ai.tool.name": "replace_file_content",
			"code.lines_added": "1", "code.lines_removed": "2", "code.file_extension": "rs",
		},
	},
	{
		name: "antigravity write_to_file", script: AntigravityHookScript, hook: "PostToolUse", tool: "antigravity", stdout: "{}",
		stdin: `{"conversationId":"ag-1","toolCall":{"name":"write_to_file","args":{"TargetFile":"/SECRET/x.md","CodeContent":"SECRET\n\nSECRET\n","Overwrite":true}}}`,
		want:  map[string]string{"code.lines_added": "3", "code.lines_removed": "0", "code.file_extension": "md"},
	},
	{
		name: "antigravity run_command", script: AntigravityHookScript, hook: "PostToolUse", tool: "antigravity", stdout: "{}",
		stdin:   `{"conversationId":"ag-1","toolCall":{"name":"run_command","args":{"CommandLine":"SECRET rm -rf","Cwd":"/SECRET"}}}`,
		want:    map[string]string{"gen_ai.tool.name": "run_command"},
		without: []string{"code.lines_added", "code.file_extension"},
	},
	{
		name: "antigravity non-tool step", script: AntigravityHookScript, hook: "PostToolUse", tool: "antigravity", stdout: "{}",
		stdin: `{"conversationId":"ag-1","toolCall":null}`, noSend: true,
	},
	{
		name: "antigravity Stop", script: AntigravityHookScript, hook: "Stop", tool: "antigravity", stdout: "{}",
		stdin: `{"conversationId":"ag-2","executionNum":1,"terminationReason":"model_stop","fullyIdle":true}`,
		want:  map[string]string{"antigravity.conversation_id": "ag-2"},
	},
	{
		name: "garbage stdin", script: CursorHookScript, hook: "stop", tool: "cursor", stdout: "{}",
		stdin: `SECRET not json`, noSend: true,
	},
}

// curlCall is what the stub curl saw.
type curlCall struct {
	args  []string
	stdin string
}

// runHook runs script with stdin and returns its stdout and the stub curl's
// call (nil when curl was not called).
func runHook(t *testing.T, script, hook, stdin string, env []string) (string, *curlCall) {
	return runHookWait(t, script, hook, stdin, env, 3*time.Second)
}

func runHookWait(t *testing.T, script, hook, stdin string, env []string, wait time.Duration) (string, *curlCall) {
	t.Helper()
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "hook.sh")
	if err := os.WriteFile(scriptPath, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0755); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "curl.args")
	stub := "#!/bin/sh\ncat > \"$CURL_OUT.stdin\"\nfor a in \"$@\"; do printf '%s\\0' \"$a\"; done > \"$CURL_OUT.tmp\" && mv \"$CURL_OUT.tmp\" \"$CURL_OUT\"\n"
	if err := os.WriteFile(filepath.Join(bin, "curl"), []byte(stub), 0755); err != nil {
		t.Fatal(err)
	}

	args := []string{scriptPath}
	if hook != "" {
		args = append(args, hook)
	}
	cmd := exec.Command("bash", args...)
	cmd.Stdin = strings.NewReader(stdin)
	cmd.Env = append([]string{
		"PATH=" + bin + string(os.PathListSeparator) + os.Getenv("PATH"),
		"HOME=" + dir,
		"CURL_OUT=" + out,
	}, env...)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		t.Fatalf("hook exited non-zero: %v", err)
	}

	// curl runs in the background; give it a moment.
	deadline := time.Now().Add(wait)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(out); err == nil {
			in, _ := os.ReadFile(out + ".stdin")
			return strings.TrimSpace(stdout.String()), &curlCall{
				args:  strings.Split(strings.TrimSuffix(string(data), "\x00"), "\x00"),
				stdin: string(in),
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	return strings.TrimSpace(stdout.String()), nil
}

func requireTools(t *testing.T) {
	t.Helper()
	for _, bin := range []string{"bash", "jq"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not installed", bin)
		}
	}
}

type otlpAttr struct {
	Key   string `json:"key"`
	Value struct {
		StringValue *string `json:"stringValue"`
		IntValue    *string `json:"intValue"`
	} `json:"value"`
}

type otlpLogs struct {
	ResourceLogs []struct {
		Resource struct {
			Attributes []otlpAttr `json:"attributes"`
		} `json:"resource"`
		ScopeLogs []struct {
			LogRecords []struct {
				TimeUnixNano string `json:"timeUnixNano"`
				Body         struct {
					StringValue string `json:"stringValue"`
				} `json:"body"`
				Attributes []otlpAttr `json:"attributes"`
			} `json:"logRecords"`
		} `json:"scopeLogs"`
	} `json:"resourceLogs"`
}

func attrMap(t *testing.T, attrs []otlpAttr) map[string]string {
	t.Helper()
	m := map[string]string{}
	for _, a := range attrs {
		switch {
		case a.Value.StringValue != nil:
			m[a.Key] = *a.Value.StringValue
		case a.Value.IntValue != nil:
			m[a.Key] = *a.Value.IntValue
		default:
			t.Fatalf("attribute %s has no string or int value", a.Key)
		}
	}
	return m
}

func TestHookScripts(t *testing.T) {
	requireTools(t)
	env := []string{"FLOWSTATE_OTLP_ENDPOINT=https://otel.example/", "FLOWSTATE_OTLP_KEY=test-key"}

	for _, tc := range hookCases {
		t.Run(tc.name, func(t *testing.T) {
			wait := 3 * time.Second
			if tc.noSend {
				wait = 300 * time.Millisecond
			}
			stdout, call := runHookWait(t, tc.script, tc.hook, tc.stdin, env, wait)
			if tc.stdout != "" && stdout != tc.stdout {
				t.Fatalf("stdout = %q, want %q", stdout, tc.stdout)
			}
			if tc.noSend {
				if call != nil {
					t.Fatalf("expected no request, curl got %q", call.args)
				}
				return
			}
			if call == nil {
				t.Fatal("curl was not called")
			}
			args := call.args

			joined := strings.Join(args, " ")
			if strings.Contains(joined+call.stdin, "SECRET") {
				t.Fatalf("request leaks content: %s", joined)
			}
			for _, want := range []string{"https://otel.example/v1/logs", "Content-Type: application/json", "--max-time", "@-"} {
				if !strings.Contains(joined, want) {
					t.Fatalf("curl args missing %q: %q", want, args)
				}
			}
			// The key must not be on curl's command line (visible in ps).
			if strings.Contains(joined, "test-key") {
				t.Fatalf("key on curl's command line: %q", args)
			}
			if call.stdin != "x-flowstate-key: test-key\n" {
				t.Fatalf("curl stdin = %q, want the key header", call.stdin)
			}

			var payload otlpLogs
			if err := json.Unmarshal([]byte(args[len(args)-1]), &payload); err != nil {
				t.Fatalf("payload is not OTLP JSON: %v\n%s", err, args[len(args)-1])
			}
			if len(payload.ResourceLogs) != 1 || len(payload.ResourceLogs[0].ScopeLogs) != 1 ||
				len(payload.ResourceLogs[0].ScopeLogs[0].LogRecords) != 1 {
				t.Fatalf("want exactly one log record: %+v", payload)
			}
			res := attrMap(t, payload.ResourceLogs[0].Resource.Attributes)
			if len(res) != 1 || res["service.name"] != tc.tool {
				t.Fatalf("resource attributes = %v", res)
			}
			rec := payload.ResourceLogs[0].ScopeLogs[0].LogRecords[0]
			attrs := attrMap(t, rec.Attributes)
			event := attrs["event.name"]
			if !strings.HasPrefix(event, tc.tool+".hook.") || rec.Body.StringValue != event {
				t.Fatalf("event.name %q / body %q", event, rec.Body.StringValue)
			}
			if tc.hook != "" && event != tc.tool+".hook."+tc.hook {
				t.Fatalf("event.name = %q", event)
			}
			if len(rec.TimeUnixNano) < 19 {
				t.Fatalf("timeUnixNano = %q", rec.TimeUnixNano)
			}
			for k, v := range tc.want {
				if attrs[k] != v {
					t.Errorf("%s = %q, want %q (all: %v)", k, attrs[k], v, attrs)
				}
			}
			for _, k := range tc.without {
				if _, ok := attrs[k]; ok {
					t.Errorf("%s must be absent (all: %v)", k, attrs)
				}
			}
			for k, v := range attrs {
				if strings.Contains(v, "/") && k != "gen_ai.request.model" {
					t.Errorf("attribute %s looks like a path: %q", k, v)
				}
			}
		})
	}
}

func TestHookScripts_EndpointAndKeyFallback(t *testing.T) {
	requireTools(t)
	// No env vars: the script must read the env file and default the path.
	confHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", confHome)
	if err := WriteEnvFile(config.Config{Endpoint: "https://file.example", Key: "file-key"}); err != nil {
		t.Fatal(err)
	}
	_, call := runHook(t, CursorHookScript, "stop", `{"conversation_id":"c"}`, []string{"XDG_CONFIG_HOME=" + confHome})
	if !containsArg(call.args, "https://file.example/v1/logs") || call.stdin != "x-flowstate-key: file-key\n" {
		t.Fatalf("env file not used: %q / %q", call.args, call.stdin)
	}

	// A stale FLOWSTATE_OTLP_ENDPOINT naming the retired receiver (an editor
	// started from a shell before the profile was rewritten) is ignored in
	// favour of the env file.
	_, call = runHook(t, CursorHookScript, "stop", `{"conversation_id":"c"}`,
		[]string{"XDG_CONFIG_HOME=" + confHome, "FLOWSTATE_OTLP_ENDPOINT=https://otel.flowstate.inc"})
	if !containsArg(call.args, "https://file.example/v1/logs") {
		t.Fatalf("legacy env endpoint used: %q", call.args)
	}

	// Nothing configured at all: default endpoint, no key header.
	_, call = runHook(t, WindsurfHookScript, "post_cascade_response", `{"trajectory_id":"t"}`, nil)
	joined := strings.Join(call.args, " ")
	if !containsArg(call.args, "https://otel.flowstate.host/v1/logs") || strings.Contains(joined, "@-") || call.stdin != "" {
		t.Fatalf("default endpoint wrong: %q / %q", call.args, call.stdin)
	}

	// A legacy endpoint everywhere (env and an old env file) falls back to
	// the default.
	legacyHome := t.TempDir()
	if err := os.MkdirAll(filepath.Join(legacyHome, "flowstate-telemetry"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacyHome, "flowstate-telemetry", "env"),
		[]byte("FLOWSTATE_OTLP_ENDPOINT=https://otel.flowstate.inc/v1/logs\nFLOWSTATE_OTLP_KEY=k\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, script := range []string{CursorHookScript, WindsurfHookScript, AntigravityHookScript} {
		_, call = runHook(t, script, "stop", `{"conversation_id":"c","trajectory_id":"t","conversationId":"a"}`,
			[]string{"XDG_CONFIG_HOME=" + legacyHome, "FLOWSTATE_OTLP_ENDPOINT=otel.flowstate.inc:4317"})
		if call == nil || !containsArg(call.args, "https://otel.flowstate.host/v1/logs") {
			t.Fatalf("legacy endpoint not replaced by the default: %+v", call)
		}
	}

	// An endpoint that already names the logs path is not doubled.
	_, call = runHook(t, AntigravityHookScript, "Stop", `{"conversationId":"a"}`,
		[]string{"FLOWSTATE_OTLP_ENDPOINT=https://x.example/v1/logs"})
	if !containsArg(call.args, "https://x.example/v1/logs") {
		t.Fatalf("endpoint wrong: %q", call.args)
	}
}

func containsArg(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}

func TestHookScripts_MirroredCopiesMatch(t *testing.T) {
	for name, embedded := range map[string]string{
		"flowstate-cursor.sh":      CursorHookScript,
		"flowstate-windsurf.sh":    WindsurfHookScript,
		"flowstate-antigravity.sh": AntigravityHookScript,
	} {
		mirror, err := os.ReadFile(filepath.Join("..", "..", "scripts", "hooks", name))
		if err != nil {
			t.Fatal(err)
		}
		if string(mirror) != embedded {
			t.Errorf("scripts/hooks/%s differs from internal/hooks/scripts/%s", name, name)
		}
	}
}
