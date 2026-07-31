package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/welworx/smartmeter-fetch/internal/config"
)

// isolateConfigDir points config.Dir() at a fresh temp directory for the
// duration of the test. HOME alone isn't enough: os.UserConfigDir() prefers
// $XDG_CONFIG_HOME on Linux, and CI runners commonly have it set, which
// would otherwise make every "isolated" test share one real config dir.
func isolateConfigDir(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, ".config"))
	t.Setenv("SMARTMETER_CONFIG_DIR", "")
}

// withStdin temporarily replaces os.Stdin with content, for tests that
// exercise promptLine (which reads one line from stdin). Restored via
// t.Cleanup. Not usable for promptSecret, which requires a real TTY.
func withStdin(t *testing.T, content string) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.WriteString(content); err != nil {
		t.Fatal(err)
	}
	w.Close()
	orig := os.Stdin
	os.Stdin = r
	t.Cleanup(func() {
		os.Stdin = orig
		r.Close()
	})
}

func TestReadPassphraseFromEnv(t *testing.T) {
	t.Setenv("SMARTMETER_PASSPHRASE", "envpass")
	got, err := readPassphrase(false)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "envpass" {
		t.Fatalf("got %q", got)
	}
}

func TestApplyProfileFields(t *testing.T) {
	secrets := []config.Profile{{Name: "main", Provider: "evn", Username: "alice", Password: "pw1"}}
	applyProfileFields(secrets, 0, "", "", "pw2")
	if secrets[0].Provider != "evn" || secrets[0].Username != "alice" || secrets[0].Password != "pw2" {
		t.Fatalf("password-only update: got %+v", secrets[0])
	}
	applyProfileFields(secrets, 0, "otherprovider", "bob", "")
	if secrets[0].Provider != "otherprovider" || secrets[0].Username != "bob" || secrets[0].Password != "pw2" {
		t.Fatalf("provider+username update: got %+v", secrets[0])
	}
}

// TestRunProfileAddFromEnv also covers finding: "add" must verify the
// credentials via a real login attempt (here, the stub) before saving, and
// default the provider to "evn" when -provider isn't given.
func TestRunProfileAddFromEnv(t *testing.T) {
	isolateConfigDir(t)
	t.Setenv("SMARTMETER_PASSPHRASE", "pp")
	t.Setenv("SMARTMETER_USER", "alice")
	t.Setenv("SMARTMETER_PASSWORD", "pw1")
	withStubProvider(t, &stubProvider{})

	var stdout, stderr bytes.Buffer
	if got := runProfile([]string{"add", "main"}, &stdout, &stderr); got != 0 {
		t.Fatalf("runProfile(add) = %d, stderr = %s", got, stderr.String())
	}
	dir, err := config.Dir()
	if err != nil {
		t.Fatal(err)
	}
	secrets, err := config.LoadSecrets(dir, []byte("pp"))
	if err != nil || len(secrets) != 1 || secrets[0].Provider != "evn" || secrets[0].Username != "alice" || secrets[0].Password != "pw1" {
		t.Fatalf("secrets = %+v, err = %v", secrets, err)
	}
}

func TestRunProfileAddDuplicateRejected(t *testing.T) {
	isolateConfigDir(t)
	t.Setenv("SMARTMETER_PASSPHRASE", "pp")
	t.Setenv("SMARTMETER_USER", "alice")
	t.Setenv("SMARTMETER_PASSWORD", "pw1")
	withStubProvider(t, &stubProvider{})

	var stdout, stderr bytes.Buffer
	runProfile([]string{"add", "main"}, &stdout, &stderr)
	if got := runProfile([]string{"add", "main"}, &stdout, &stderr); got != 1 {
		t.Fatalf("duplicate runProfile(add) = %d, want 1", got)
	}
}

// TestRunProfileAddLoginFailureNotSaved covers finding: a rejected login
// (e.g. wrong password) must abort before writing anything to credentials.enc.
func TestRunProfileAddLoginFailureNotSaved(t *testing.T) {
	isolateConfigDir(t)
	t.Setenv("SMARTMETER_PASSPHRASE", "pp")
	t.Setenv("SMARTMETER_USER", "alice")
	t.Setenv("SMARTMETER_PASSWORD", "wrong")
	withStubProvider(t, &stubProvider{err: errStub})

	var stdout, stderr bytes.Buffer
	if got := runProfile([]string{"add", "main"}, &stdout, &stderr); got != 1 {
		t.Fatalf("runProfile(add) with rejected login = %d, want 1", got)
	}
	if !strings.Contains(stderr.String(), "login to evn failed") {
		t.Errorf("stderr = %q, want login-failed message", stderr.String())
	}
	dir, _ := config.Dir()
	if config.CredentialsExist(dir) {
		t.Fatal("credentials.enc should not exist after a rejected login")
	}
}

func TestRunProfileAddUnknownProvider(t *testing.T) {
	isolateConfigDir(t)
	t.Setenv("SMARTMETER_PASSPHRASE", "pp")
	t.Setenv("SMARTMETER_USER", "alice")
	t.Setenv("SMARTMETER_PASSWORD", "pw1")

	var stdout, stderr bytes.Buffer
	if got := runProfile([]string{"add", "main", "-provider", "bogus"}, &stdout, &stderr); got != 1 {
		t.Fatalf("runProfile(add -provider bogus) = %d, want 1", got)
	}
	if !strings.Contains(stderr.String(), `unknown provider "bogus"`) {
		t.Errorf("stderr = %q, want unknown provider message", stderr.String())
	}
}

func TestRunProfileListEmptyNoPrompt(t *testing.T) {
	isolateConfigDir(t)
	// No SMARTMETER_PASSPHRASE set, and no credentials.enc yet: must not
	// try to prompt (which would fail/hang with no TTY in a test).
	var stdout, stderr bytes.Buffer
	if got := runProfile([]string{"list"}, &stdout, &stderr); got != 0 {
		t.Fatalf("runProfile(list) on empty dir = %d, want 0", got)
	}
}

func TestRunProfileListAfterAdd(t *testing.T) {
	isolateConfigDir(t)
	t.Setenv("SMARTMETER_PASSPHRASE", "pp")
	t.Setenv("SMARTMETER_USER", "alice")
	t.Setenv("SMARTMETER_PASSWORD", "pw1")
	withStubProvider(t, &stubProvider{})
	var stdout, stderr bytes.Buffer
	runProfile([]string{"add", "main"}, &stdout, &stderr)

	stdout.Reset()
	if got := runProfile([]string{"list"}, &stdout, &stderr); got != 0 {
		t.Fatalf("runProfile(list) = %d, want 0", got)
	}
	if !strings.Contains(stdout.String(), "main\tevn\talice") {
		t.Fatalf("stdout = %q, want profile listed with provider", stdout.String())
	}
}

func TestRunProfileUpdateUsernameBlankKeepsCurrent(t *testing.T) {
	isolateConfigDir(t)
	t.Setenv("SMARTMETER_PASSPHRASE", "pp")
	t.Setenv("SMARTMETER_USER", "alice")
	t.Setenv("SMARTMETER_PASSWORD", "pw1")
	withStubProvider(t, &stubProvider{})
	var stdout, stderr bytes.Buffer
	runProfile([]string{"add", "main"}, &stdout, &stderr)

	t.Setenv("SMARTMETER_USER", "")
	t.Setenv("SMARTMETER_PASSWORD", "pw2")
	withStdin(t, "\n")
	if got := runProfile([]string{"update", "main"}, &stdout, &stderr); got != 0 {
		t.Fatalf("runProfile(update) = %d, want 0, stderr = %s", got, stderr.String())
	}
	dir, _ := config.Dir()
	secrets, err := config.LoadSecrets(dir, []byte("pp"))
	if err != nil || len(secrets) != 1 || secrets[0].Provider != "evn" || secrets[0].Username != "alice" || secrets[0].Password != "pw2" {
		t.Fatalf("secrets = %+v, err = %v", secrets, err)
	}
}

// TestRunProfileUpdateLoginFailureNotSaved covers finding: "update" must
// re-verify the resulting (possibly merged) credentials before persisting,
// leaving the previously-stored profile untouched on failure.
func TestRunProfileUpdateLoginFailureNotSaved(t *testing.T) {
	isolateConfigDir(t)
	t.Setenv("SMARTMETER_PASSPHRASE", "pp")
	t.Setenv("SMARTMETER_USER", "alice")
	t.Setenv("SMARTMETER_PASSWORD", "pw1")
	withStubProvider(t, &stubProvider{})
	var stdout, stderr bytes.Buffer
	runProfile([]string{"add", "main"}, &stdout, &stderr)

	t.Setenv("SMARTMETER_PASSWORD", "wrong")
	withStubProvider(t, &stubProvider{err: errStub})
	if got := runProfile([]string{"update", "main"}, &stdout, &stderr); got != 1 {
		t.Fatalf("runProfile(update) with rejected login = %d, want 1", got)
	}
	dir, _ := config.Dir()
	secrets, err := config.LoadSecrets(dir, []byte("pp"))
	if err != nil || len(secrets) != 1 || secrets[0].Password != "pw1" {
		t.Fatalf("secrets should be unchanged: %+v, err = %v", secrets, err)
	}
}

func TestRunProfileUpdateMissing(t *testing.T) {
	isolateConfigDir(t)
	// No credentials.enc at all: must fail before any prompt.
	var stdout, stderr bytes.Buffer
	if got := runProfile([]string{"update", "ghost"}, &stdout, &stderr); got != 1 {
		t.Fatalf("runProfile(update ghost) on empty dir = %d, want 1", got)
	}
}

func TestRunProfileRemove(t *testing.T) {
	isolateConfigDir(t)
	t.Setenv("SMARTMETER_PASSPHRASE", "pp")
	t.Setenv("SMARTMETER_USER", "alice")
	t.Setenv("SMARTMETER_PASSWORD", "pw1")
	withStubProvider(t, &stubProvider{})
	var stdout, stderr bytes.Buffer
	runProfile([]string{"add", "main"}, &stdout, &stderr)

	if got := runProfile([]string{"remove", "main"}, &stdout, &stderr); got != 0 {
		t.Fatalf("runProfile(remove) = %d, want 0", got)
	}
	dir, _ := config.Dir()
	secrets, err := config.LoadSecrets(dir, []byte("pp"))
	if err != nil || len(secrets) != 0 {
		t.Fatalf("secrets = %+v, err = %v, want empty", secrets, err)
	}
}

func TestRunProfileRemoveMissing(t *testing.T) {
	isolateConfigDir(t)
	var stdout, stderr bytes.Buffer
	if got := runProfile([]string{"remove", "ghost"}, &stdout, &stderr); got != 1 {
		t.Fatalf("runProfile(remove ghost) on empty dir = %d, want 1", got)
	}
}

func TestProfileChangePassphraseNoCredentials(t *testing.T) {
	dir := t.TempDir()
	if err := profileChangePassphrase(dir); err == nil {
		t.Fatal("expected error rekeying a directory with no credentials.enc")
	}
}

func TestRunProfileEmptyArgs(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if got := runProfile(nil, &stdout, &stderr); got != 2 {
		t.Fatalf("runProfile(nil) = %d, want 2 (usage)", got)
	}
}

func TestRunProfileDirError(t *testing.T) {
	// No SMARTMETER_CONFIG_DIR override and no $HOME: config.Dir() must
	// fail, and runProfile must surface that before dispatching.
	t.Setenv("SMARTMETER_CONFIG_DIR", "")
	t.Setenv("HOME", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	var stdout, stderr bytes.Buffer
	if got := runProfile([]string{"list"}, &stdout, &stderr); got != 1 {
		t.Fatalf("runProfile(list) with no $HOME = %d, want 1", got)
	}
}

func TestRunProfileAddUsage(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if got := runProfile([]string{"add"}, &stdout, &stderr); got != 2 {
		t.Fatalf("runProfile(add) with no name = %d, want 2", got)
	}
}

func TestRunProfileAddWrongPassphrase(t *testing.T) {
	isolateConfigDir(t)
	t.Setenv("SMARTMETER_PASSPHRASE", "right")
	t.Setenv("SMARTMETER_USER", "alice")
	t.Setenv("SMARTMETER_PASSWORD", "pw1")
	withStubProvider(t, &stubProvider{})
	var stdout, stderr bytes.Buffer
	runProfile([]string{"add", "main"}, &stdout, &stderr)

	t.Setenv("SMARTMETER_PASSPHRASE", "wrong")
	if got := runProfile([]string{"add", "second"}, &stdout, &stderr); got != 1 {
		t.Fatalf("runProfile(add) with wrong passphrase = %d, want 1", got)
	}
}

func TestRunProfileAddUsernamePromptError(t *testing.T) {
	isolateConfigDir(t)
	t.Setenv("SMARTMETER_PASSPHRASE", "pp")
	t.Setenv("SMARTMETER_USER", "")
	t.Setenv("SMARTMETER_PASSWORD", "pw1")
	withStdin(t, "") // no trailing newline: promptLine's ReadString hits EOF
	var stdout, stderr bytes.Buffer
	if got := runProfile([]string{"add", "main"}, &stdout, &stderr); got != 1 {
		t.Fatalf("runProfile(add) with username prompt EOF = %d, want 1", got)
	}
}

func TestRunProfileUpdateUsage(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if got := runProfile([]string{"update"}, &stdout, &stderr); got != 2 {
		t.Fatalf("runProfile(update) with no name = %d, want 2", got)
	}
}

func TestRunProfileUpdateWrongPassphrase(t *testing.T) {
	isolateConfigDir(t)
	t.Setenv("SMARTMETER_PASSPHRASE", "right")
	t.Setenv("SMARTMETER_USER", "alice")
	t.Setenv("SMARTMETER_PASSWORD", "pw1")
	withStubProvider(t, &stubProvider{})
	var stdout, stderr bytes.Buffer
	runProfile([]string{"add", "main"}, &stdout, &stderr)

	t.Setenv("SMARTMETER_PASSPHRASE", "wrong")
	if got := runProfile([]string{"update", "main"}, &stdout, &stderr); got != 1 {
		t.Fatalf("runProfile(update) with wrong passphrase = %d, want 1", got)
	}
}

func TestRunProfileUpdateNotFoundWithCredentials(t *testing.T) {
	isolateConfigDir(t)
	t.Setenv("SMARTMETER_PASSPHRASE", "pp")
	t.Setenv("SMARTMETER_USER", "alice")
	t.Setenv("SMARTMETER_PASSWORD", "pw1")
	withStubProvider(t, &stubProvider{})
	var stdout, stderr bytes.Buffer
	runProfile([]string{"add", "main"}, &stdout, &stderr)

	if got := runProfile([]string{"update", "ghost"}, &stdout, &stderr); got != 1 {
		t.Fatalf("runProfile(update ghost) = %d, want 1", got)
	}
}

func TestRunProfileListWrongPassphrase(t *testing.T) {
	isolateConfigDir(t)
	t.Setenv("SMARTMETER_PASSPHRASE", "right")
	t.Setenv("SMARTMETER_USER", "alice")
	t.Setenv("SMARTMETER_PASSWORD", "pw1")
	withStubProvider(t, &stubProvider{})
	var stdout, stderr bytes.Buffer
	runProfile([]string{"add", "main"}, &stdout, &stderr)

	t.Setenv("SMARTMETER_PASSPHRASE", "wrong")
	if got := runProfile([]string{"list"}, &stdout, &stderr); got != 1 {
		t.Fatalf("runProfile(list) with wrong passphrase = %d, want 1", got)
	}
}

func TestRunProfileRemoveUsage(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if got := runProfile([]string{"remove"}, &stdout, &stderr); got != 2 {
		t.Fatalf("runProfile(remove) with no name = %d, want 2", got)
	}
}

func TestRunProfileRemoveNotFoundAmongExisting(t *testing.T) {
	isolateConfigDir(t)
	t.Setenv("SMARTMETER_PASSPHRASE", "pp")
	t.Setenv("SMARTMETER_USER", "alice")
	t.Setenv("SMARTMETER_PASSWORD", "pw1")
	withStubProvider(t, &stubProvider{})
	var stdout, stderr bytes.Buffer
	runProfile([]string{"add", "main"}, &stdout, &stderr)
	t.Setenv("SMARTMETER_USER", "bob")
	t.Setenv("SMARTMETER_PASSWORD", "pw2")
	runProfile([]string{"add", "second"}, &stdout, &stderr)

	if got := runProfile([]string{"remove", "ghost"}, &stdout, &stderr); got != 1 {
		t.Fatalf("runProfile(remove ghost) among existing profiles = %d, want 1", got)
	}
	dir, _ := config.Dir()
	secrets, err := config.LoadSecrets(dir, []byte("pp"))
	if err != nil || len(secrets) != 2 {
		t.Fatalf("secrets should be unchanged: %+v, err = %v", secrets, err)
	}
}

func TestRunProfilePassphraseNoCredentials(t *testing.T) {
	isolateConfigDir(t)
	var stdout, stderr bytes.Buffer
	if got := runProfile([]string{"passphrase"}, &stdout, &stderr); got != 1 {
		t.Fatalf("runProfile(passphrase) with no credentials.enc = %d, want 1", got)
	}
}

func TestRunProfileUnknownSubcommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if got := runProfile([]string{"bogus"}, &stdout, &stderr); got != 2 {
		t.Fatalf("runProfile(bogus) = %d, want 2 (usage)", got)
	}
}

func TestRunProfileRejectsLeftoverArgs(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"add with extra arg", []string{"add", "main", "extra"}},
		{"remove with extra arg", []string{"remove", "main", "extra"}},
		{"list with extra arg", []string{"list", "extra"}},
		{"passphrase with extra arg", []string{"passphrase", "extra"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if got := runProfile(tc.args, &stdout, &stderr); got != 2 {
				t.Fatalf("runProfile(%v) = %d, want 2 (usage)", tc.args, got)
			}
		})
	}
}
