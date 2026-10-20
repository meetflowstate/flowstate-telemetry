package tools

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/meetflowstate/flowstate-telemetry/internal/config"
	"github.com/meetflowstate/flowstate-telemetry/internal/shell"
)

// readJSONFile reads a JSON object from path; a missing file is an empty map.
func readJSONFile(path string) (map[string]interface{}, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return make(map[string]interface{}), nil
		}
		return nil, fmt.Errorf("reading %s: %w", filepath.Base(path), err)
	}
	var out map[string]interface{}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", filepath.Base(path), err)
	}
	if out == nil {
		out = make(map[string]interface{})
	}
	return out, nil
}

// writeJSONFile writes v as indented JSON, creating the parent directory.
func writeJSONFile(path string, v map[string]interface{}, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("creating directory: %w", err)
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling %s: %w", filepath.Base(path), err)
	}
	return os.WriteFile(path, append(data, '\n'), mode)
}

// tightenMode removes group and other permissions from path when it has
// any, for files that hold the key: 0644 becomes 0600. It never loosens a
// mode (0400 stays 0400). A symlink's target is changed.
func tightenMode(path string) error {
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	if perm := fi.Mode().Perm(); perm&0o077 != 0 {
		if err := os.Chmod(path, perm&^0o077); err != nil {
			return fmt.Errorf("restricting %s to its owner: %w", path, err)
		}
	}
	return nil
}

// writeKeyFile writes a file that holds the key: a new file is 0600 and an
// existing one is tightened (tightenMode). Like os.WriteFile it writes
// through a symlink (a dotfiles-managed file stays a symlink).
func writeKeyFile(path string, data []byte) error {
	if err := os.WriteFile(path, data, 0600); err != nil {
		return err
	}
	return tightenMode(path)
}

// fileExists reports whether path exists (following symlinks).
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// redactKey hides the Flowstate key in dry-run output.
func redactKey(s, key string) string {
	if key == "" {
		return s
	}
	return strings.ReplaceAll(s, key, "<redacted>")
}

// hookCommand is the hooks.json command for one event: the script path
// (quoted when it contains spaces) followed by the event name, so scripts
// know which hook fired even when the payload does not say.
func hookCommand(scriptPath, event string) string {
	p := scriptPath
	if strings.ContainsAny(p, " \t'\"") {
		p = "'" + strings.ReplaceAll(p, "'", `'\''`) + "'"
	}
	return p + " " + event
}

// isFlowstateHook reports whether a hooks.json entry runs our script.
func isFlowstateHook(entry interface{}, scriptPath string) bool {
	m, ok := entry.(map[string]interface{})
	if !ok {
		return false
	}
	cmd, _ := m["command"].(string)
	return cmd != "" && strings.Contains(cmd, scriptPath)
}

// stripFlowstateHooks removes our entries from every event in a flat
// {event: [{command}]} hooks block (Cursor, Windsurf), dropping events left
// empty. Entries from earlier releases (other events, bare script path)
// are removed too.
func stripFlowstateHooks(block map[string]interface{}, scriptPath string) {
	for event, raw := range block {
		list, ok := raw.([]interface{})
		if !ok {
			continue
		}
		var kept []interface{}
		for _, e := range list {
			if !isFlowstateHook(e, scriptPath) {
				kept = append(kept, e)
			}
		}
		if len(kept) == 0 {
			delete(block, event)
		} else {
			block[event] = kept
		}
	}
}

// addFlowstateHooks strips our old entries, then appends one entry per event,
// preserving every other hook. extra is merged into each entry.
func addFlowstateHooks(block map[string]interface{}, scriptPath string, events []string, extra map[string]interface{}) {
	stripFlowstateHooks(block, scriptPath)
	for _, event := range events {
		list, _ := block[event].([]interface{})
		entry := map[string]interface{}{"command": hookCommand(scriptPath, event)}
		for k, v := range extra {
			entry[k] = v
		}
		block[event] = append(list, entry)
	}
}

// hasFlowstateHook reports whether event in a flat hooks block runs our script.
func hasFlowstateHook(block map[string]interface{}, scriptPath, event string) bool {
	list, _ := block[event].([]interface{})
	for _, e := range list {
		if isFlowstateHook(e, scriptPath) {
			return true
		}
	}
	return false
}

// commandAvailable reports whether name is on PATH.
func commandAvailable(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// joinWarnings joins non-empty warnings for VerifyResult.Warning.
func joinWarnings(w []string) string {
	return strings.Join(w, "; ")
}

// shellProfileName is the shell profile path, for messages.
func shellProfileName() string {
	return shell.ProfilePath()
}

// resourceAttrsAssignment returns the scoped OTEL_RESOURCE_ATTRIBUTES
// assignment for a wrapper function, or nil when there are none.
func resourceAttrsAssignment(cfg config.Config) []string {
	attrs := shell.ResourceAttributes(cfg)
	if attrs == "" {
		return nil
	}
	return []string{shell.EnvAssignment("OTEL_RESOURCE_ATTRIBUTES", attrs)}
}

// mergeResourceAttrs returns the OTEL_RESOURCE_ATTRIBUTES value existing
// with this CLI's flowstate.* entries replaced by ours ("" removes them),
// keeping the user's own entries in their order. Values are percent-encoded
// (shell.EncodeResourceValue), so ',' only ever separates entries.
func mergeResourceAttrs(existing, ours string) string {
	var kept []string
	for _, kv := range strings.Split(existing, ",") {
		kv = strings.TrimSpace(kv)
		if kv == "" || isFlowstateAttr(kv) {
			continue
		}
		kept = append(kept, kv)
	}
	if ours != "" {
		kept = append(kept, ours)
	}
	return strings.Join(kept, ",")
}

// isFlowstateAttr reports whether one key=value resource attribute is one
// this CLI writes. The key is compared percent-decoded.
func isFlowstateAttr(kv string) bool {
	k, _, _ := strings.Cut(kv, "=")
	k = strings.TrimSpace(k)
	if d, err := url.PathUnescape(k); err == nil {
		k = d
	}
	return k == "flowstate.team_id" || k == "flowstate.email"
}

// setResourceAttrs merges ours into env["OTEL_RESOURCE_ATTRIBUTES"] (see
// mergeResourceAttrs), deleting the key when nothing is left. A non-string
// value is the user's and is left alone.
func setResourceAttrs(env map[string]interface{}, ours string) {
	raw, present := env["OTEL_RESOURCE_ATTRIBUTES"]
	existing, isString := raw.(string)
	if present && !isString {
		return
	}
	if merged := mergeResourceAttrs(existing, ours); merged != "" {
		env["OTEL_RESOURCE_ATTRIBUTES"] = merged
	} else {
		delete(env, "OTEL_RESOURCE_ATTRIBUTES")
	}
}

// isFlowstateResourceAttrs reports whether an OTEL_RESOURCE_ATTRIBUTES value
// is one this CLI wrote.
func isFlowstateResourceAttrs(v interface{}) bool {
	s, _ := v.(string)
	return strings.Contains(s, "flowstate.team_id=") || strings.Contains(s, "flowstate.email=")
}

// legacyEndpoint returns a failing VerifyResult when endpoint is the retired
// otel.flowstate.inc receiver, or nil.
func legacyEndpoint(where, endpoint string) *VerifyResult {
	if !config.IsLegacyEndpoint(endpoint) {
		return nil
	}
	return &VerifyResult{OK: false, Message: fmt.Sprintf("%s is the retired receiver %s: re-run `flowstate-telemetry install`", where, endpoint)}
}

// promptsOnWarning is the verify warning for a tool that logs prompt or
// response content. Flowstate drops content anyway; it should not leave
// the machine.
func promptsOnWarning(setting string) string {
	return setting + " is on: prompt content leaves the machine (Flowstate drops it); re-run install without --prompts"
}
