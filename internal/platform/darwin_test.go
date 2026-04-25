//go:build darwin

package platform

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// fakeRunner records every command invocation and returns a programmable
// sequence of (output, error) pairs keyed by command name + first arg.
type fakeRunner struct {
	mu       sync.Mutex
	calls    []call
	responses map[string]response
}

type call struct {
	name string
	args []string
}

type response struct {
	out []byte
	err error
}

func newFakeRunner() *fakeRunner {
	return &fakeRunner{responses: make(map[string]response)}
}

// SetResponse registers a canned response for `name <firstArg>`. If no entry
// matches, the runner returns no output and no error (success default).
func (f *fakeRunner) SetResponse(key string, out []byte, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.responses[key] = response{out: out, err: err}
}

func (f *fakeRunner) Run(name string, args ...string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, call{name: name, args: append([]string(nil), args...)})

	key := name
	if len(args) > 0 {
		key = name + " " + args[0]
	}
	if r, ok := f.responses[key]; ok {
		return r.out, r.err
	}
	if r, ok := f.responses[name]; ok {
		return r.out, r.err
	}
	return nil, nil
}

// Calls returns a snapshot of recorded invocations. Each entry is a
// space-joined "name args[0] args[1] ..." for easy assertion.
func (f *fakeRunner) Calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.calls))
	for i, c := range f.calls {
		out[i] = c.name + " " + strings.Join(c.args, " ")
	}
	return out
}

// TestDarwin_InstallTrust_ShellsOutCorrectly asserts the exact `security`
// invocation we rely on for trust install. If this string drifts, MDM
// playbooks and our docs need updating in lockstep.
func TestDarwin_InstallTrust_ShellsOutCorrectly(t *testing.T) {
	runner := newFakeRunner()
	p := &darwinPlatform{runner: runner}

	if err := p.InstallTrust("/tmp/ca.crt"); err != nil {
		t.Fatalf("InstallTrust: %v", err)
	}

	calls := runner.Calls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %v", calls)
	}
	want := "security add-trusted-cert -d -r trustRoot -k " + systemKeychain + " /tmp/ca.crt"
	if calls[0] != want {
		t.Errorf("call mismatch:\n got: %s\nwant: %s", calls[0], want)
	}
}

// TestDarwin_InstallTrust_PropagatesError surfaces the underlying shell
// error so install commands fail fast.
func TestDarwin_InstallTrust_PropagatesError(t *testing.T) {
	runner := newFakeRunner()
	runner.SetResponse("security add-trusted-cert", []byte("permission denied"), errors.New("exit 1"))
	p := &darwinPlatform{runner: runner}

	err := p.InstallTrust("/tmp/ca.crt")
	if err == nil {
		t.Fatal("expected error to propagate")
	}
	if !strings.Contains(err.Error(), "permission denied") {
		t.Errorf("expected combined output in error, got: %v", err)
	}
}

// TestDarwin_InstallTrust_RequiresPath rejects empty input.
func TestDarwin_InstallTrust_RequiresPath(t *testing.T) {
	p := &darwinPlatform{runner: newFakeRunner()}
	if err := p.InstallTrust(""); err == nil {
		t.Fatal("expected error for empty path")
	}
}

// TestDarwin_RemoveTrust_TreatsNotFoundAsSuccess covers the idempotency
// contract — removing a cert that is no longer present must not error.
func TestDarwin_RemoveTrust_TreatsNotFoundAsSuccess(t *testing.T) {
	runner := newFakeRunner()
	runner.SetResponse("security remove-trusted-cert", []byte("certificate not found"), errors.New("exit 1"))
	p := &darwinPlatform{runner: runner}

	if err := p.RemoveTrust("aabbcc"); err != nil {
		t.Errorf("expected nil for not-found, got: %v", err)
	}
}

// TestDarwin_RemoveTrust_PropagatesUnknownErrors keeps the not-found special
// case from swallowing real failures.
func TestDarwin_RemoveTrust_PropagatesUnknownErrors(t *testing.T) {
	runner := newFakeRunner()
	runner.SetResponse("security remove-trusted-cert", []byte("disk full"), errors.New("exit 5"))
	p := &darwinPlatform{runner: runner}

	if err := p.RemoveTrust("aabbcc"); err == nil {
		t.Error("expected error for unknown failure")
	}
}

// TestDarwin_InstallPAC_FansOutToEveryService asserts we register PAC on
// every networksetup-known service.
func TestDarwin_InstallPAC_FansOutToEveryService(t *testing.T) {
	runner := newFakeRunner()
	listing := "An asterisk (*) denotes that a network service is disabled.\nWi-Fi\nEthernet\n*Disabled Service\n"
	runner.SetResponse("networksetup -listallnetworkservices", []byte(listing), nil)
	p := &darwinPlatform{runner: runner}

	if err := p.InstallPAC("http://127.0.0.1:47813/proxy.pac"); err != nil {
		t.Fatalf("InstallPAC: %v", err)
	}

	calls := runner.Calls()
	wantCalls := []string{
		"networksetup -listallnetworkservices",
		"networksetup -setautoproxyurl Wi-Fi http://127.0.0.1:47813/proxy.pac",
		"networksetup -setautoproxyurl Ethernet http://127.0.0.1:47813/proxy.pac",
	}
	if len(calls) != len(wantCalls) {
		t.Fatalf("calls = %v, want %v", calls, wantCalls)
	}
	for i, w := range wantCalls {
		if calls[i] != w {
			t.Errorf("call[%d] = %q, want %q", i, calls[i], w)
		}
	}
}

// TestDarwin_InstallPAC_SkipsDisabledServices is implicit in the previous
// test (the *Disabled entry didn't get a call), but we keep an explicit
// check that a list with only disabled services is a no-op.
func TestDarwin_InstallPAC_SkipsDisabledServices(t *testing.T) {
	runner := newFakeRunner()
	runner.SetResponse("networksetup -listallnetworkservices",
		[]byte("An asterisk denotes...\n*Off1\n*Off2\n"), nil)
	p := &darwinPlatform{runner: runner}

	if err := p.InstallPAC("http://x"); err != nil {
		t.Fatalf("InstallPAC: %v", err)
	}
	for _, c := range runner.Calls() {
		if strings.HasPrefix(c, "networksetup -setautoproxyurl") {
			t.Errorf("did not expect setautoproxyurl call, got %q", c)
		}
	}
}

// TestDarwin_RemovePAC_DisablesEachService verifies we use -setautoproxystate
// off (not just clear the URL), which is the idempotent disable path.
func TestDarwin_RemovePAC_DisablesEachService(t *testing.T) {
	runner := newFakeRunner()
	runner.SetResponse("networksetup -listallnetworkservices",
		[]byte("An asterisk denotes...\nWi-Fi\n"), nil)
	p := &darwinPlatform{runner: runner}

	if err := p.RemovePAC(); err != nil {
		t.Fatalf("RemovePAC: %v", err)
	}
	want := "networksetup -setautoproxystate Wi-Fi off"
	found := false
	for _, c := range runner.Calls() {
		if c == want {
			found = true
		}
	}
	if !found {
		t.Errorf("expected call %q, got %v", want, runner.Calls())
	}
}

// TestDarwin_InstallService_WritesPlistAndBootstraps uses a temp path for
// the plist drop so we don't actually try to write under /Library.
func TestDarwin_InstallService_WritesPlistAndBootstraps(t *testing.T) {
	dir := t.TempDir()
	tmpPlist := filepath.Join(dir, "inc.flowstate.telemetry.proxy.plist")
	// Override the package-level launchDaemonPath via a small indirection:
	// for the test we patch the value, then restore it on cleanup.
	orig := getLaunchDaemonPath()
	setLaunchDaemonPath(tmpPlist)
	t.Cleanup(func() { setLaunchDaemonPath(orig) })

	runner := newFakeRunner()
	p := &darwinPlatform{runner: runner}

	execPath := "/usr/local/bin/flowstate-telemetry"
	if err := p.InstallService(execPath); err != nil {
		t.Fatalf("InstallService: %v", err)
	}

	// Plist must exist with the exec path embedded.
	contents, err := os.ReadFile(tmpPlist)
	if err != nil {
		t.Fatalf("read plist: %v", err)
	}
	if !strings.Contains(string(contents), execPath) {
		t.Errorf("plist missing execPath, got:\n%s", contents)
	}
	if !strings.Contains(string(contents), launchDaemonLabel) {
		t.Errorf("plist missing label, got:\n%s", contents)
	}

	// launchctl bootstrap must have been called.
	wantBootstrap := fmt.Sprintf("launchctl bootstrap system %s", tmpPlist)
	found := false
	for _, c := range runner.Calls() {
		if c == wantBootstrap {
			found = true
		}
	}
	if !found {
		t.Errorf("expected %q in calls, got %v", wantBootstrap, runner.Calls())
	}
}

// TestDarwin_InstallService_ReloadOnConflict simulates an already-loaded
// daemon: bootstrap fails on the first call, the platform should bootout
// and retry.
func TestDarwin_InstallService_ReloadOnConflict(t *testing.T) {
	dir := t.TempDir()
	tmpPlist := filepath.Join(dir, "plist")
	orig := getLaunchDaemonPath()
	setLaunchDaemonPath(tmpPlist)
	t.Cleanup(func() { setLaunchDaemonPath(orig) })

	runner := &flakyBootstrapRunner{newFakeRunner(), 0}
	p := &darwinPlatform{runner: runner}
	if err := p.InstallService("/usr/local/bin/flowstate-telemetry"); err != nil {
		t.Fatalf("InstallService: %v", err)
	}
	// We expect at least: bootstrap (fail), bootout, bootstrap (succeed).
	count := 0
	for _, c := range runner.fakeRunner.Calls() {
		if strings.HasPrefix(c, "launchctl bootstrap") {
			count++
		}
	}
	if count != 2 {
		t.Errorf("expected 2 bootstrap attempts, got %d (%v)", count, runner.fakeRunner.Calls())
	}
}

// TestDarwin_UninstallService_RemovesPlistAndIsIdempotent runs uninstall
// twice; the second call must succeed even though the file is gone.
func TestDarwin_UninstallService_RemovesPlistAndIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	tmpPlist := filepath.Join(dir, "plist")
	if err := os.WriteFile(tmpPlist, []byte("dummy"), 0o644); err != nil {
		t.Fatalf("seed plist: %v", err)
	}
	orig := getLaunchDaemonPath()
	setLaunchDaemonPath(tmpPlist)
	t.Cleanup(func() { setLaunchDaemonPath(orig) })

	runner := newFakeRunner()
	p := &darwinPlatform{runner: runner}
	if err := p.UninstallService(); err != nil {
		t.Fatalf("UninstallService: %v", err)
	}
	if _, err := os.Stat(tmpPlist); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("expected plist removed, stat err = %v", err)
	}
	// Second call: must NOT error even though the file is gone.
	if err := p.UninstallService(); err != nil {
		t.Errorf("second UninstallService: %v", err)
	}
}

// flakyBootstrapRunner returns an error the first time `bootstrap` is invoked
// and succeeds on subsequent attempts. Drives the reload-on-conflict branch.
type flakyBootstrapRunner struct {
	*fakeRunner
	bootstrapCalls int
}

func (f *flakyBootstrapRunner) Run(name string, args ...string) ([]byte, error) {
	if name == "launchctl" && len(args) > 0 && args[0] == "bootstrap" {
		f.bootstrapCalls++
		// Record the call.
		_, _ = f.fakeRunner.Run(name, args...)
		if f.bootstrapCalls == 1 {
			return []byte("Service already loaded"), errors.New("exit 1")
		}
		return nil, nil
	}
	return f.fakeRunner.Run(name, args...)
}

// setLaunchDaemonPath is a tiny test helper that sets the package-level
// override. Defined here to keep the override surface in darwin.go small.
func setLaunchDaemonPath(p string) { launchDaemonPathTestOverride = p }
