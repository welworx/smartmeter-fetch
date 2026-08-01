package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// hitLog records request paths hit on the mock server, safe for concurrent
// handler goroutines.
type hitLog struct {
	mu    sync.Mutex
	paths []string
}

func (h *hitLog) add(p string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.paths = append(h.paths, p)
}

func (h *hitLog) hasPrefix(prefix string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, p := range h.paths {
		if strings.HasPrefix(p, prefix) {
			return true
		}
	}
	return false
}

// newUpgradeTestServer serves a fake releases/latest response for tagName,
// with a matching platform asset (content binContent) and SHA256SUMS.txt.
// If corruptSum is true, the published checksum doesn't match binContent.
func newUpgradeTestServer(t *testing.T, tagName string, binContent []byte, corruptSum bool) (*httptest.Server, *hitLog) {
	t.Helper()
	hits := &hitLog{}
	assetName := platformAssetName()

	sum := sha256.Sum256(binContent)
	sumHex := hex.EncodeToString(sum[:])
	if corruptSum {
		sumHex = strings.Repeat("0", 64)
	}
	sumsContent := fmt.Sprintf("%s  %s\n", sumHex, assetName)

	mux := http.NewServeMux()
	var srv *httptest.Server
	mux.HandleFunc("/repos/welworx/smartmeter-fetch/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		hits.add(r.URL.Path)
		rel := ghRelease{
			TagName: tagName,
			Assets: []ghAsset{
				{Name: assetName, BrowserDownloadURL: srv.URL + "/download/" + assetName},
				{Name: "SHA256SUMS.txt", BrowserDownloadURL: srv.URL + "/download/SHA256SUMS.txt"},
			},
		}
		_ = json.NewEncoder(w).Encode(rel)
	})
	mux.HandleFunc("/download/"+assetName, func(w http.ResponseWriter, r *http.Request) {
		hits.add(r.URL.Path)
		_, _ = w.Write(binContent)
	})
	mux.HandleFunc("/download/SHA256SUMS.txt", func(w http.ResponseWriter, r *http.Request) {
		hits.add(r.URL.Path)
		_, _ = w.Write([]byte(sumsContent))
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, hits
}

// newUpgradeTestServerNoMatchingAsset serves a release whose only asset
// name can never match platformAssetName(), simulating an unsupported
// platform.
func newUpgradeTestServerNoMatchingAsset(t *testing.T, tagName string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/welworx/smartmeter-fetch/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		rel := ghRelease{
			TagName: tagName,
			Assets: []ghAsset{
				{Name: "smartmeter-fetch-plan9-386", BrowserDownloadURL: "http://unused.invalid/asset"},
				{Name: "SHA256SUMS.txt", BrowserDownloadURL: "http://unused.invalid/sums"},
			},
		}
		_ = json.NewEncoder(w).Encode(rel)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func useTestGithubAPI(t *testing.T, url string) {
	t.Helper()
	orig := githubAPIBase
	githubAPIBase = url
	t.Cleanup(func() { githubAPIBase = orig })
}

func writeScratchExecutable(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	target := filepath.Join(dir, "smartmeter-fetch")
	if err := os.WriteFile(target, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return target
}

func assertNoLeftoverFiles(t *testing.T, target string) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Dir(target))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != filepath.Base(target) {
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		t.Fatalf("leftover files in target dir: %v", names)
	}
}

func TestDoUpgradeNewerVersionConfirmed(t *testing.T) {
	binContent := []byte("new binary content")
	srv, hits := newUpgradeTestServer(t, "v0.2.0", binContent, false)
	useTestGithubAPI(t, srv.URL)
	target := writeScratchExecutable(t, "old binary content")
	withStdin(t, "y\n")

	var stdout, stderr bytes.Buffer
	got := doUpgrade(false, false, target, "v0.1.0", &stdout, &stderr)
	if got != 0 {
		t.Fatalf("doUpgrade = %d, want 0; stdout=%q stderr=%q", got, stdout.String(), stderr.String())
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(binContent) {
		t.Fatalf("target content = %q, want %q", data, binContent)
	}
	assertNoLeftoverFiles(t, target)
	if !hits.hasPrefix("/download/") {
		t.Fatal("expected download requests to have been made")
	}
}

func TestRenameAsideRollback(t *testing.T) {
	target := writeScratchExecutable(t, "original content")
	dir := filepath.Dir(target)

	tmp, err := writeTempBinary(dir, []byte("new content"))
	if err != nil {
		t.Fatal(err)
	}
	// Force the second rename (tmp -> target) to fail.
	if err := os.Remove(tmp); err != nil {
		t.Fatal(err)
	}

	if err := renameAside(tmp, target); err == nil {
		t.Fatal("renameAside: expected error")
	}

	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "original content" {
		t.Fatalf("target = %q, want restored original", data)
	}
	if _, err := os.Stat(target + ".old"); !os.IsNotExist(err) {
		t.Fatalf(".old file left behind after rollback")
	}
}

func TestDoUpgradeSameVersionUpToDate(t *testing.T) {
	srv, hits := newUpgradeTestServer(t, "v0.2.0", []byte("irrelevant"), false)
	useTestGithubAPI(t, srv.URL)
	target := writeScratchExecutable(t, "unchanged")

	var stdout, stderr bytes.Buffer
	if got := doUpgrade(false, false, target, "v0.2.0", &stdout, &stderr); got != 0 {
		t.Fatalf("doUpgrade = %d, want 0", got)
	}
	if !strings.Contains(stdout.String(), "up to date") {
		t.Fatalf("stdout = %q, want up-to-date message", stdout.String())
	}
	if hits.hasPrefix("/download/") {
		t.Fatal("no download should have been attempted")
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "unchanged" {
		t.Fatal("target was modified")
	}
}

func TestDoUpgradeOlderVersionUpToDate(t *testing.T) {
	srv, hits := newUpgradeTestServer(t, "v0.1.0", []byte("irrelevant"), false)
	useTestGithubAPI(t, srv.URL)
	target := writeScratchExecutable(t, "unchanged")

	var stdout, stderr bytes.Buffer
	if got := doUpgrade(false, false, target, "v0.2.0", &stdout, &stderr); got != 0 {
		t.Fatalf("doUpgrade = %d, want 0", got)
	}
	if hits.hasPrefix("/download/") {
		t.Fatal("no download should have been attempted")
	}
}

func TestDoUpgradeDevVersionAlwaysUpgradable(t *testing.T) {
	binContent := []byte("release binary")
	// Tag is lower than any real version dev would compare against, but
	// dev must upgrade anyway - it never parses as semver for comparison.
	srv, _ := newUpgradeTestServer(t, "v0.0.1", binContent, false)
	useTestGithubAPI(t, srv.URL)
	target := writeScratchExecutable(t, "old binary")

	var stdout, stderr bytes.Buffer
	// -y so no stdin read is needed here.
	if got := doUpgrade(false, true, target, "dev", &stdout, &stderr); got != 0 {
		t.Fatalf("doUpgrade = %d, want 0", got)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(binContent) {
		t.Fatalf("target content = %q, want %q", data, binContent)
	}
}

func TestDoUpgradeChecksumMismatchAborts(t *testing.T) {
	srv, _ := newUpgradeTestServer(t, "v0.2.0", []byte("new binary"), true /* corrupt sum */)
	useTestGithubAPI(t, srv.URL)
	target := writeScratchExecutable(t, "old binary")
	withStdin(t, "y\n")

	var stdout, stderr bytes.Buffer
	got := doUpgrade(false, false, target, "v0.1.0", &stdout, &stderr)
	if got == 0 {
		t.Fatal("doUpgrade = 0, want non-zero on checksum mismatch")
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "old binary" {
		t.Fatalf("target was modified despite checksum mismatch: %q", data)
	}
	assertNoLeftoverFiles(t, target)
}

func TestDoUpgradeCheckReportsAvailability(t *testing.T) {
	srv, hits := newUpgradeTestServer(t, "v0.2.0", []byte("irrelevant"), false)
	useTestGithubAPI(t, srv.URL)
	target := writeScratchExecutable(t, "unchanged")

	var stdout, stderr bytes.Buffer
	if got := doUpgrade(true, false, target, "v0.1.0", &stdout, &stderr); got != 1 {
		t.Fatalf("doUpgrade(-check) = %d, want 1", got)
	}
	if !strings.Contains(stdout.String(), "v0.2.0") {
		t.Fatalf("stdout = %q, want it to mention the available version", stdout.String())
	}
	if hits.hasPrefix("/download/") {
		t.Fatal("-check must not download anything")
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "unchanged" {
		t.Fatal("-check must not touch the target")
	}
}

func TestDoUpgradeCheckWinsOverYes(t *testing.T) {
	srv, hits := newUpgradeTestServer(t, "v0.2.0", []byte("irrelevant"), false)
	useTestGithubAPI(t, srv.URL)
	target := writeScratchExecutable(t, "unchanged")

	var stdout, stderr bytes.Buffer
	// -check and -y together: -check wins, -y is ignored, no download.
	if got := doUpgrade(true, true, target, "v0.1.0", &stdout, &stderr); got != 1 {
		t.Fatalf("doUpgrade(-check -y) = %d, want 1", got)
	}
	if hits.hasPrefix("/download/") {
		t.Fatal("-check must not download anything, even with -y")
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "unchanged" {
		t.Fatal("-check must not touch the target")
	}
}

func TestDoUpgradeCheckErrorExitCode(t *testing.T) {
	// No server override - githubAPIBase points nowhere reachable in test
	// isolation, but to keep this hermetic point it at a server that 404s.
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	useTestGithubAPI(t, srv.URL)
	target := writeScratchExecutable(t, "unchanged")

	var stdout, stderr bytes.Buffer
	if got := doUpgrade(true, false, target, "v0.1.0", &stdout, &stderr); got != 2 {
		t.Fatalf("doUpgrade(-check) on error = %d, want 2", got)
	}
}

func TestDoUpgradeMissingAssetForPlatform(t *testing.T) {
	srv := newUpgradeTestServerNoMatchingAsset(t, "v0.2.0")
	useTestGithubAPI(t, srv.URL)
	target := writeScratchExecutable(t, "unchanged")

	var stdout, stderr bytes.Buffer
	got := doUpgrade(false, false, target, "v0.1.0", &stdout, &stderr)
	if got == 0 {
		t.Fatal("doUpgrade = 0, want non-zero for missing platform asset")
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "unchanged" {
		t.Fatal("target was modified despite missing asset")
	}
}

func TestDoUpgradeYesSkipsPrompt(t *testing.T) {
	binContent := []byte("new binary")
	srv, _ := newUpgradeTestServer(t, "v0.2.0", binContent, false)
	useTestGithubAPI(t, srv.URL)
	target := writeScratchExecutable(t, "old binary")

	// stdin is an empty, already-closed pipe: any attempt to read it (i.e.
	// -y not actually skipping the prompt) yields EOF, which doUpgrade
	// treats as an error - so a wrongly-shown prompt fails this test
	// instead of silently passing.
	withStdin(t, "")

	var stdout, stderr bytes.Buffer
	got := doUpgrade(false, true, target, "v0.1.0", &stdout, &stderr)
	if got != 0 {
		t.Fatalf("doUpgrade(-y) = %d, want 0 (prompt should have been skipped)", got)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(binContent) {
		t.Fatal("target not updated")
	}
}

func TestVersionNewer(t *testing.T) {
	cases := []struct {
		current, latest string
		want            bool
	}{
		{"v0.1.0", "v0.2.0", true},
		{"v0.2.0", "v0.1.0", false},
		{"v0.2.0", "v0.2.0", false},
		{"v0.2.0", "v0.2.1", true},
		{"v0.2.1", "v0.2.0", false},
		{"v1.0.0", "v0.99.99", false},
	}
	for _, c := range cases {
		got, err := versionNewer(c.current, c.latest)
		if err != nil {
			t.Fatalf("versionNewer(%q, %q): %v", c.current, c.latest, err)
		}
		if got != c.want {
			t.Errorf("versionNewer(%q, %q) = %v, want %v", c.current, c.latest, got, c.want)
		}
	}
}

func TestVerifyChecksum(t *testing.T) {
	data := []byte("hello world")
	sum := sha256.Sum256(data)
	sumHex := hex.EncodeToString(sum[:])

	sums := []byte(sumHex + "  smartmeter-fetch-linux-amd64\nsomeotherhash  someotherfile\n")
	if err := verifyChecksum(data, sums, "smartmeter-fetch-linux-amd64"); err != nil {
		t.Fatalf("verifyChecksum: %v", err)
	}

	badSums := []byte(strings.Repeat("0", 64) + "  smartmeter-fetch-linux-amd64\n")
	if err := verifyChecksum(data, badSums, "smartmeter-fetch-linux-amd64"); err == nil {
		t.Fatal("verifyChecksum: expected mismatch error")
	}

	if err := verifyChecksum(data, sums, "smartmeter-fetch-darwin-arm64"); err == nil {
		t.Fatal("verifyChecksum: expected not-listed error")
	}
}

func TestRunUpgradeUnknownFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if got := runUpgrade([]string{"-bogus"}, &stdout, &stderr); got != 2 {
		t.Fatalf("runUpgrade(-bogus) = %d, want 2", got)
	}
}

func TestRunUpgradeRejectsUnexpectedArgument(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if got := runUpgrade([]string{"-check", "bogus"}, &stdout, &stderr); got != 2 {
		t.Fatalf("runUpgrade(-check bogus) = %d, want 2", got)
	}
}
