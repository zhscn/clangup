package clangup

import (
	"bytes"
	"strings"
	"testing"
)

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

func TestRepoCommandIsAbsent(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if exitCode := Run([]string{"repo"}, &stdout, &stderr, "test"); exitCode != 2 {
		t.Fatalf("exit code = %d", exitCode)
	}
}
