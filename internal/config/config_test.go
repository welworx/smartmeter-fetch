package config

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestSecretsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	pass := []byte("hunter2")
	profiles := []Profile{
		{Name: "main", Username: "alice", Password: "pw-a"},
		{Name: "second", Username: "bob", Password: "pw-b"},
	}
	if err := SaveSecrets(dir, pass, profiles); err != nil {
		t.Fatal(err)
	}
	got, err := LoadSecrets(dir, pass)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != profiles[0] || got[1] != profiles[1] {
		t.Fatalf("got %+v, want %+v", got, profiles)
	}
}

func TestSecretsWrongPassphrase(t *testing.T) {
	dir := t.TempDir()
	if err := SaveSecrets(dir, []byte("right"), []Profile{{Name: "a", Password: "b"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSecrets(dir, []byte("wrong")); err == nil {
		t.Fatal("wrong passphrase decrypted successfully")
	}
}

func TestSecretsMissingFile(t *testing.T) {
	got, err := LoadSecrets(t.TempDir(), []byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("got %v, want empty", got)
	}
}

func TestSaveSecretsAtomicNoLeftoverTmp(t *testing.T) {
	dir := t.TempDir()
	pass := []byte("hunter2")
	profiles := []Profile{{Name: "main", Username: "alice", Password: "pw-a"}}
	if err := SaveSecrets(dir, pass, profiles); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(credPath(dir) + ".tmp"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("leftover .tmp file after SaveSecrets: err = %v", err)
	}
	got, err := LoadSecrets(dir, pass)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != profiles[0] {
		t.Fatalf("got %+v, want %+v", got, profiles)
	}
}

func TestLoadSecretsReadError(t *testing.T) {
	dir := t.TempDir()
	// credentials.enc as a directory: os.ReadFile fails with a non-
	// ErrNotExist error, distinct from the "file missing" branch.
	if err := os.Mkdir(credPath(dir), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSecrets(dir, []byte("x")); err == nil {
		t.Fatal("expected error reading credentials.enc as a directory")
	}
}

func TestLoadSecretsCorruptPayload(t *testing.T) {
	dir := t.TempDir()
	pass := []byte("hunter2")
	blob, err := encrypt(pass, []byte("not json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(credPath(dir), blob, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSecrets(dir, pass); err == nil {
		t.Fatal("expected json unmarshal error for corrupt payload")
	}
}

func TestLoadSecretsUnsupportedVersion(t *testing.T) {
	dir := t.TempDir()
	pass := []byte("hunter2")
	blob, err := encrypt(pass, []byte("{}"))
	if err != nil {
		t.Fatal(err)
	}
	blob[0] = 99
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(credPath(dir), blob, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSecrets(dir, pass); err == nil {
		t.Fatal("expected unsupported-version error")
	}
}

func TestSaveSecretsMkdirAllError(t *testing.T) {
	// A regular file where a path component is expected to be a directory:
	// os.MkdirAll fails (ENOTDIR), no permission tricks needed.
	base := t.TempDir()
	regularFile := filepath.Join(base, "not-a-dir")
	if err := os.WriteFile(regularFile, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(regularFile, "sub")
	if err := SaveSecrets(dir, []byte("hunter2"), []Profile{{Name: "main", Password: "pw"}}); err == nil {
		t.Fatal("expected error creating credentials dir under a regular file")
	}
}

func TestDecryptCorruptShortBlob(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(credPath(dir), []byte{1, 2, 3}, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSecrets(dir, []byte("x")); err == nil {
		t.Fatal("expected error for too-short credentials.enc")
	}
}

func TestDirRespectsConfigDirOverride(t *testing.T) {
	want := filepath.Join(t.TempDir(), "custom")
	t.Setenv("SMARTMETER_CONFIG_DIR", want)
	got, err := Dir()
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("Dir() = %q, want %q", got, want)
	}
}

func TestDirErrorsWithoutHome(t *testing.T) {
	// No SMARTMETER_CONFIG_DIR override and no $HOME: os.UserConfigDir()
	// fails on darwin/unix, and Dir() must propagate that. $XDG_CONFIG_HOME
	// is cleared too since os.UserConfigDir() checks it before $HOME on
	// Linux, where a CI runner commonly has it set.
	t.Setenv("SMARTMETER_CONFIG_DIR", "")
	t.Setenv("HOME", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	if _, err := Dir(); err == nil {
		t.Fatal("expected error when $HOME is unset")
	}
}

func TestSecretsSurviveRekey(t *testing.T) {
	dir := t.TempDir()
	old := []byte("old-pass")
	profiles := []Profile{{Name: "main", Username: "alice", Password: "pw1"}}
	if err := SaveSecrets(dir, old, profiles); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadSecrets(dir, old)
	if err != nil {
		t.Fatal(err)
	}

	newPass := []byte("new-pass")
	if err := SaveSecrets(dir, newPass, loaded); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSecrets(dir, old); err == nil {
		t.Fatal("old passphrase still works after rekey")
	}
	got, err := LoadSecrets(dir, newPass)
	if err != nil || len(got) != 1 || got[0] != profiles[0] {
		t.Fatalf("got %+v, err = %v", got, err)
	}
}
