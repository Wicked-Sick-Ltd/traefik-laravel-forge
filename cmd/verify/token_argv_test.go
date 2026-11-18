package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

// tokenPlaceholder is an obvious stand-in, not a Forge credential.
const tokenPlaceholder = "placeholder-not-a-real-forge-token"

const exampleOrg = "example-org"

// tokenFlagLine matches the help line Go's flag package prints for a flag
// named token. See flag.FlagSet.PrintDefaults in the Go 1.22 standard library.
var tokenFlagLine = regexp.MustCompile(`(?m)^\s+-token(\s|$)`)

// These tests are the spec for keeping the Forge API token off the process
// command line. They fail while cmd/verify still accepts -token and passes
// that value to the Forge client.

func TestMain(m *testing.M) {
	if os.Getenv("VERIFY_AS_MAIN") == "1" {
		// The production client leaves Transport nil, so net/http uses
		// DefaultTransport when the request is sent. Replace it before main
		// builds that request so the test records the header and does not
		// dial the network.
		http.DefaultTransport = recordingTransport{}
		main()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// recordingTransport records the Authorization header and URL of the request
// the production client passes to RoundTrip, then returns an empty server
// list so GenerateConfiguration and DumpRaw can finish.
type recordingTransport struct{}

func (recordingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if path := os.Getenv("VERIFY_AUTH_FILE"); path != "" {
		payload := req.Header.Get("Authorization") + "\n" + req.URL.String() + "\n"
		if err := os.WriteFile(path, []byte(payload), 0o600); err != nil { //nolint:gosec // path is the temp file this test created
			return nil, err
		}
	}
	if release := os.Getenv("VERIFY_RELEASE_FILE"); release != "" {
		deadline := time.Now().Add(30 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(release); err == nil { //nolint:gosec // path is the temp file this test created
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	body := []byte(`{"data":[],"links":[],"meta":[]}`)
	return &http.Response{
		StatusCode:    http.StatusOK,
		Status:        "200 OK",
		Header:        make(http.Header),
		Body:          io.NopCloser(bytes.NewReader(body)),
		ContentLength: int64(len(body)),
		Request:       req,
	}, nil
}

type verifyResult struct {
	auth             string
	url              string
	exit             int
	cmdline          string
	cmdlineMode      os.FileMode
	nobodyCmdline    string
	nobodyCmdErr     string
	nobodyEnvironErr string
	procVisible      bool
	stdout           string
	stderr           string
}

func TestVerifyCommandLineTokenIsNotTheForgeBearer(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{name: "space separated", args: []string{"-token", tokenPlaceholder, "-org", exampleOrg}},
		{name: "double dash", args: []string{"--token", tokenPlaceholder, "-org", exampleOrg}},
		{name: "equals form", args: []string{"-token=" + tokenPlaceholder, "-org", exampleOrg}},
		{name: "dump", args: []string{"-token", tokenPlaceholder, "-org", exampleOrg, "-dump"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := runAsVerify(t, tc.args, nil)
			assertCommandLineTokenRejected(t, got)
		})
	}
}

func TestVerifyHelpDoesNotPublishTheForgeTokenFlag(t *testing.T) {
	got := runAsVerify(t, []string{"-h"}, []string{"FORGE_TOKEN=" + tokenPlaceholder, "FORGE_ORG=" + exampleOrg})
	if tokenFlagLine.MatchString(got.stderr) {
		t.Error("verify help still offers a -token flag")
	}
	if strings.Contains(got.stderr, tokenPlaceholder) || strings.Contains(got.stdout, tokenPlaceholder) {
		t.Error("verify help printed the Forge token taken from the environment")
	}
}

func TestVerifySourceDoesNotDocumentTokenFlag(t *testing.T) {
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(src, []byte("-token")) || bytes.Contains(src, []byte(`flag.String("token"`)) {
		t.Fatal("cmd/verify/main.go still documents or registers a token flag")
	}
}

func TestVerifyEnvironmentTokenIsSentAndNotOnTheCommandLine(t *testing.T) {
	got := runAsVerify(t, []string{"-org", exampleOrg}, []string{
		"FORGE_TOKEN=" + tokenPlaceholder,
		"FORGE_ORG=" + exampleOrg,
	})
	wantAuth := "Bearer " + tokenPlaceholder
	if got.auth != wantAuth {
		t.Fatalf("environment token was not sent as the Forge bearer (exit %d, url %q, stderr %q)", got.exit, got.url, got.stderr)
	}
	if !strings.Contains(got.url, "https://forge.laravel.com/api/orgs/"+exampleOrg+"/servers") {
		t.Fatalf("bearer was sent to %q, want the Forge servers endpoint", got.url)
	}
	// /proc is a Linux process table. macOS and Windows still have to prove the
	// bearer came from the environment; they have no command line to inspect.
	if runtime.GOOS == "linux" && !got.procVisible {
		t.Fatal("linux command line was not readable")
	}
	if !got.procVisible {
		return
	}
	if strings.Contains(got.cmdline, tokenPlaceholder) || strings.Contains(got.nobodyCmdline, tokenPlaceholder) {
		t.Fatal("environment token appeared on the verify process command line")
	}
	if got.cmdlineMode&0o004 == 0 {
		t.Fatalf("cmdline mode is %o; the other-user read did not exercise a world-readable command line", got.cmdlineMode)
	}
	if got.nobodyCmdErr != "" {
		t.Fatalf("another local user could not read the verify command line: %s", got.nobodyCmdErr)
	}
	if got.nobodyEnvironErr == "" {
		t.Fatal("another local user read the verify process environment")
	}
}

func assertCommandLineTokenRejected(t *testing.T, got verifyResult) {
	t.Helper()
	if got.auth == "Bearer "+tokenPlaceholder {
		t.Fatalf("verify sent the command-line token as the Forge bearer to %s (cmdline mode %o, owner cmdline has token: %v, other user cmdline has token: %v, other user error: %s)",
			got.url,
			got.cmdlineMode,
			strings.Contains(got.cmdline, tokenPlaceholder),
			strings.Contains(got.nobodyCmdline, tokenPlaceholder),
			got.nobodyCmdErr,
		)
	}
	if got.exit == 0 {
		t.Fatalf("verify exited 0 when a token was passed on the command line; stderr=%q", got.stderr)
	}
}

func runAsVerify(t *testing.T, args []string, extraEnv []string) verifyResult {
	t.Helper()
	dir := t.TempDir()
	authPath := filepath.Join(dir, "auth")
	releasePath := filepath.Join(dir, "release")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], args...) //nolint:gosec // re-exec this test binary to call main with a real argv
	cmd.Env = verifyChildEnv(append([]string{
		"VERIFY_AS_MAIN=1",
		"VERIFY_AUTH_FILE=" + authPath,
		"VERIFY_RELEASE_FILE=" + releasePath,
	}, extraEnv...))
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start verify: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	var got verifyResult
	timer := time.NewTimer(20 * time.Second)
	defer timer.Stop()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()

	release := func() {
		if err := os.WriteFile(releasePath, []byte("go"), 0o600); err != nil {
			t.Errorf("release verify: %v", err)
		}
	}

	finished := false
	for !finished {
		select {
		case err := <-done:
			got.exit = exitCode(t, err)
			finished = true
		case <-ticker.C:
			body, err := os.ReadFile(authPath) //nolint:gosec // path is the temp file this test created
			if err != nil {
				continue
			}
			got.auth, got.url = splitRecord(string(body))
			cmdline, mode, visible, readErr := readCmdline(cmd.Process.Pid)
			if readErr != nil {
				release()
				t.Fatalf("read cmdline: %v", readErr)
			}
			got.cmdline, got.cmdlineMode, got.procVisible = cmdline, mode, visible
			if visible {
				nobodyCmd, nobodyErr := readProcAsNobody(cmd.Process.Pid, "cmdline")
				got.nobodyCmdline = nobodyCmd
				if nobodyErr != nil {
					got.nobodyCmdErr = nobodyErr.Error()
				}
				_, envErr := readProcAsNobody(cmd.Process.Pid, "environ")
				if envErr != nil {
					got.nobodyEnvironErr = envErr.Error()
				}
			}
			release()
			select {
			case err := <-done:
				got.exit = exitCode(t, err)
			case <-timer.C:
				_ = cmd.Process.Kill()
				t.Fatal("verify did not exit after the request was recorded")
			}
			finished = true
		case <-timer.C:
			release()
			_ = cmd.Process.Kill()
			t.Fatal("timed out waiting for verify")
		}
	}
	if body, err := os.ReadFile(authPath); err == nil && got.auth == "" { //nolint:gosec // path is the temp file this test created
		got.auth, got.url = splitRecord(string(body))
	}
	got.stdout = stdout.String()
	got.stderr = stderr.String()
	return got
}

func splitRecord(body string) (auth, rawURL string) {
	auth, rawURL, _ = strings.Cut(body, "\n")
	rawURL = strings.TrimSuffix(rawURL, "\n")
	return auth, rawURL
}

func exitCode(t *testing.T, err error) int {
	t.Helper()
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	t.Fatalf("wait for verify: %v", err)
	return -1
}

// readCmdline reads the child command line from the Linux process table.
// Other operating systems have no /proc, so the caller skips those checks
// and still asserts on the Forge bearer.
func readCmdline(pid int) (string, os.FileMode, bool, error) {
	if runtime.GOOS != "linux" {
		return "", 0, false, nil
	}
	path := fmt.Sprintf("/proc/%d/cmdline", pid)
	body, err := os.ReadFile(path) //nolint:gosec // path is /proc/<pid>/cmdline for the child this test started
	if err != nil {
		if os.IsNotExist(err) {
			return "", 0, false, nil
		}
		return "", 0, false, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", 0, false, err
	}
	return string(body), info.Mode().Perm(), true, nil
}

func readProcAsNobody(pid int, name string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sudo", "-n", "-u", "nobody", "cat", fmt.Sprintf("/proc/%d/%s", pid, name)) //nolint:gosec // reads the child process this test started
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func verifyChildEnv(extra []string) []string {
	skip := map[string]bool{
		"FORGE_TOKEN": true,
		"FORGE_ORG":   true,
		"HTTP_PROXY":  true,
		"HTTPS_PROXY": true,
		"http_proxy":  true,
		"https_proxy": true,
		"ALL_PROXY":   true,
		"all_proxy":   true,
	}
	var env []string
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if skip[key] || strings.HasPrefix(key, "VERIFY_") {
			continue
		}
		env = append(env, entry)
	}
	return append(env, extra...)
}
