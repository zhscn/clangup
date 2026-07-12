package clangup

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zhscn/clangup/internal/clangup/toolchain"
)

func TestRepoSpecCheckJSON(t *testing.T) {
	var stdout, stderr bytes.Buffer
	exitCode := Run([]string{
		"repo", "spec", "check", defaultSpecPath(t), "--format=json",
	}, &stdout, &stderr, "test")
	if exitCode != 0 {
		t.Fatalf("exit code = %d, stderr = %s", exitCode, stderr.String())
	}
	var result specResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Schema != "clangup.repo.spec-check/v1" || result.Channel != "default" || len(result.Targets) != 3 {
		t.Fatalf("unexpected result: %#v", result)
	}
}

func TestMatchSelectorUsesLongestNamespace(t *testing.T) {
	repositories := []toolchain.Repository{
		{Namespace: "example.com", URL: "https://example.com/catalog-v1.json"},
		{Namespace: "example.com/llvm", URL: "https://example.com/llvm/catalog-v1.json"},
	}
	repository, channel, exact, err := matchSelector(repositories, "example.com/llvm/default@22.1.8-1")
	if err != nil {
		t.Fatal(err)
	}
	if repository.Namespace != "example.com/llvm" || channel != "default" || exact != "22.1.8-1" {
		t.Fatalf("unexpected match: %#v, %q, %q", repository, channel, exact)
	}
}

func TestCompareNumericVersion(t *testing.T) {
	for _, test := range []struct {
		left, right string
		want        int
	}{
		{left: "2.28", right: "2.17", want: 1},
		{left: "2.17", right: "2.17.0", want: 0},
		{left: "11.0", right: "12.0", want: -1},
	} {
		if got := compareNumericVersion(test.left, test.right); got != test.want {
			t.Fatalf("compareNumericVersion(%q, %q) = %d, want %d", test.left, test.right, got, test.want)
		}
	}
}

func TestRepoSpecLockWritesCanonicalOutput(t *testing.T) {
	output := filepath.Join(t.TempDir(), "nested", "spec.lock.json")
	var stdout, stderr bytes.Buffer
	exitCode := Run([]string{
		"repo", "spec", "lock", defaultSpecPath(t), "--output", output, "--format=json",
	}, &stdout, &stderr, "test")
	if exitCode != 0 {
		t.Fatalf("exit code = %d, stderr = %s", exitCode, stderr.String())
	}
	contents, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(contents) || !strings.Contains(string(contents), `"schema":"clangup.build-lock/v1"`) {
		t.Fatalf("invalid lock output: %s", contents)
	}
	var result lockResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.SHA256) != 64 || result.Output != output {
		t.Fatalf("unexpected result: %#v", result)
	}
}

func TestRepoSpecCheckReturnsStructuredError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "invalid.yaml")
	if err := os.WriteFile(path, []byte("schema: wrong\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	exitCode := Run([]string{"repo", "spec", "check", path, "--format=json"}, &stdout, &stderr, "test")
	if exitCode != 2 {
		t.Fatalf("exit code = %d, want 2; stderr = %s", exitCode, stderr.String())
	}
	var response struct {
		Schema string `json:"schema"`
		Error  struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Schema != "clangup.error/v1" || response.Error.Code != "invalid_spec" {
		t.Fatalf("unexpected response: %#v", response)
	}
}

func TestRepoSpecLockDoesNotOverwriteAuthoringSpec(t *testing.T) {
	path := defaultSpecPath(t)
	var stdout, stderr bytes.Buffer
	exitCode := Run([]string{"repo", "spec", "lock", path, "--output", path, "--format=json"}, &stdout, &stderr, "test")
	if exitCode != 2 {
		t.Fatalf("exit code = %d, want 2; stderr = %s", exitCode, stderr.String())
	}
	if !strings.Contains(stdout.String(), "must not overwrite") {
		t.Fatalf("unexpected error: %s", stdout.String())
	}
}

func TestUnknownCommandIsInvalidRequest(t *testing.T) {
	var stdout, stderr bytes.Buffer
	exitCode := Run([]string{"unknown", "--format=json"}, &stdout, &stderr, "test")
	if exitCode != 2 {
		t.Fatalf("exit code = %d, want 2; stderr = %s", exitCode, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"code":"invalid_request"`) {
		t.Fatalf("unexpected error: %s", stdout.String())
	}
}

func defaultSpecPath(t *testing.T) string {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("..", "..", "specs", "default", "22.1.8", "spec.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	return path
}
