package cmk

import (
	"runtime"
	"strings"
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

func TestRecipeEnvExportsResolvedJobs(t *testing.T) {
	t.Setenv("CMK_DEFAULT_JOBS", "7")
	p := &Project{Root: "/project", Cfg: &Config{Deps: map[string]*DepCfg{}}}
	env, err := recipeEnv(p, "demo", &DepCfg{}, &Toolchain{CC: "cc", CXX: "c++"}, "/prefix", "/work", "")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, entry := range env {
		if entry == "CMK_JOBS=7" {
			found = true
		}
		if strings.HasPrefix(entry, "CMK_DEFAULT_JOBS=") {
			t.Fatalf("recipe environment leaks user setting as an interface variable: %q", entry)
		}
	}
	if !found {
		t.Fatal("recipe environment does not contain CMK_JOBS=7")
	}
}
