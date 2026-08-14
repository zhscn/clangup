package cmk

import (
	"runtime"
	"testing"
)

func TestDefaultJobsEnvironment(t *testing.T) {
	t.Setenv("CMK_DEFAULT_JOBS", "7")
	t.Setenv("CMK_JOBS", "99")
	if got := defaultJobs(); got != 7 {
		t.Fatalf("defaultJobs() = %d, want 7", got)
	}

	t.Setenv("CMK_DEFAULT_JOBS", "invalid")
	want := max(runtime.NumCPU()-1, 1)
	if got := defaultJobs(); got != want {
		t.Fatalf("defaultJobs() with invalid value = %d, want %d", got, want)
	}
}
