package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtractTfwFlags(t *testing.T) {
	o := defaultOpts()
	rest := extractTfwFlags([]string{"-var-file=x.tfvars", "--color=never", "-target=a.b", "--plan-dir", "out", "-auto-approve"}, o)
	want := []string{"-var-file=x.tfvars", "-target=a.b", "-auto-approve"}
	if strings.Join(rest, " ") != strings.Join(want, " ") {
		t.Fatalf("rest = %v, want %v", rest, want)
	}
	if o.colorMode != "never" || o.planDir != "out" || o.force {
		t.Fatalf("opts = %+v", o)
	}

	// everything after "--" is verbatim, even tfw-looking flags
	o2 := defaultOpts()
	rest2 := extractTfwFlags([]string{"--force", "--", "-force", "x"}, o2)
	if strings.Join(rest2, " ") != "-force x" {
		t.Fatalf("rest2 = %v", rest2)
	}
	if !o2.force {
		t.Fatal("--force before -- should still apply to tfw")
	}
}

func TestTerragruntFlag(t *testing.T) {
	for _, spelling := range []string{"--terragrunt", "-terragrunt", "--tg", "-tg"} {
		o := defaultOpts()
		rest := extractTfwFlags([]string{spelling, "-var=x"}, o)
		if !o.terragrunt {
			t.Fatalf("%s did not select terragrunt", spelling)
		}
		if strings.Join(rest, " ") != "-var=x" {
			t.Fatalf("%s: rest = %v, terragrunt flags must not reach the tool", spelling, rest)
		}
		if got := o.tool().Name; got != "terragrunt" {
			t.Fatalf("%s: tool = %q", spelling, got)
		}
	}
	// terragrunt's own flags are NOT tfw's: they must pass through untouched
	o := defaultOpts()
	rest := extractTfwFlags([]string{"--terragrunt", "--terragrunt-working-dir", "envs/prod", "-auto-approve"}, o)
	if strings.Join(rest, " ") != "--terragrunt-working-dir envs/prod -auto-approve" {
		t.Fatalf("rest = %v", rest)
	}
	if o.tool().Name != "terragrunt" {
		t.Fatalf("tool = %q", o.tool().Name)
	}
}

func TestHoistTfwFlags(t *testing.T) {
	cases := []struct {
		args []string
		cmd  string
		rest string
	}{
		{[]string{"plan"}, "plan", ""},
		{[]string{"--terragrunt", "plan"}, "plan", "--terragrunt"},
		{[]string{"--tg", "--color", "never", "apply", "-auto-approve"}, "apply", "--tg --color never -auto-approve"},
		{[]string{"--plan-dir=out", "plan", "-var=a"}, "plan", "--plan-dir=out -var=a"},
		{[]string{"version"}, "version", ""},
		{[]string{"-h"}, "-h", ""},
		{[]string{"plan", "--terragrunt"}, "plan", "--terragrunt"},
	}
	for _, c := range cases {
		cmd, rest := hoistTfwFlags(c.args)
		if cmd != c.cmd || strings.Join(rest, " ") != c.rest {
			t.Fatalf("hoistTfwFlags(%v) = %q, %v; want %q, %q", c.args, cmd, rest, c.cmd, c.rest)
		}
	}
}

// TestApplyGate drives the built binary against a fake `terraform` so we can
// prove a destructive plan never reaches apply on the wrong word.
func TestApplyGate(t *testing.T) { testGate(t, "terraform", "SHIM-TERRAFORM-APPLY", false) }

// TestTerragruntGate is the same gate with `--terragrunt`: it also proves the
// plan file is handed to terragrunt as an absolute path, because the fake
// terragrunt resolves relative paths inside a cache dir the way the real one
// does.
func TestTerragruntGate(t *testing.T) { testGate(t, "terragrunt", "SHIM-TERRAGRUNT-APPLY", true) }

func testGate(t *testing.T, toolName, shimMarker string, terragrunt bool) {
	t.Helper()
	toolArgs := func(extra ...string) []string {
		args := append([]string{}, extra...)
		if terragrunt {
			args = append(args, "--terragrunt")
		}
		return args
	}
	otherMarker := "SHIM-TERRAGRUNT-APPLY"
	if terragrunt {
		otherMarker = "SHIM-TERRAFORM-APPLY"
	}

	tmp := t.TempDir()
	bin := filepath.Join(tmp, "tfw")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	shim, err := filepath.Abs("testdata/shim")
	if err != nil {
		t.Fatal(err)
	}
	planDir := filepath.Join(tmp, "plans")
	env := append(os.Environ(), "PATH="+shim+string(os.PathListSeparator)+os.Getenv("PATH"))

	run := func(args ...string) (string, int) {
		cmd := exec.Command(bin, args...)
		cmd.Env = env
		out, _ := cmd.CombinedOutput()
		code := cmd.ProcessState.ExitCode()
		return string(out), code
	}
	withInput := func(input string, args ...string) (string, int) {
		cmd := exec.Command(bin, args...)
		cmd.Env = env
		cmd.Stdin = strings.NewReader(input)
		out, _ := cmd.CombinedOutput()
		return string(out), cmd.ProcessState.ExitCode()
	}

	if out, code := run(toolArgs("plan", "--plan-dir", planDir)...); code != 0 {
		t.Fatalf("%s plan: exit %d\n%s", toolName, code, out)
	}

	t.Run("yes is rejected", func(t *testing.T) {
		out, code := withInput("yes\n", toolArgs("apply", "--plan-dir", planDir)...)
		if code == 0 {
			t.Fatalf("apply accepted 'yes' on a destructive plan\n%s", out)
		}
		if strings.Contains(out, shimMarker) {
			t.Fatalf("%s apply was reached with 'yes'\n%s", toolName, out)
		}
	})

	t.Run("uppercase DESTROY rejected", func(t *testing.T) {
		out, code := withInput("DESTROY\n", toolArgs("apply", "--plan-dir", planDir)...)
		if code == 0 || strings.Contains(out, shimMarker) {
			t.Fatalf("apply accepted 'DESTROY'\n%s", out)
		}
	})

	t.Run("auto-approve blocked", func(t *testing.T) {
		out, code := run(toolArgs("apply", "--plan-dir", planDir, "-auto-approve")...)
		if code == 0 || strings.Contains(out, shimMarker) {
			t.Fatalf("-auto-approve was not blocked\n%s", out)
		}
		if !strings.Contains(out, "BLOCKED") {
			t.Fatalf("expected BLOCKED message\n%s", out)
		}
	})

	if terragrunt {
		t.Run("terragrunt --non-interactive blocked", func(t *testing.T) {
			out, code := run(toolArgs("apply", "--plan-dir", planDir, "--non-interactive")...)
			if code == 0 || strings.Contains(out, shimMarker) {
				t.Fatalf("--non-interactive was not blocked\n%s", out)
			}
		})
	}

	t.Run("destroy is accepted", func(t *testing.T) {
		out, code := withInput("destroy\n", toolArgs("apply", "--plan-dir", planDir)...)
		if code != 0 {
			t.Fatalf("apply with 'destroy' failed: exit %d\n%s", code, out)
		}
		if !strings.Contains(out, shimMarker) {
			t.Fatalf("%s apply was not reached\n%s", toolName, out)
		}
		if strings.Contains(out, otherMarker) {
			t.Fatalf("wrong tool was reached\n%s", out)
		}
	})

	t.Run("check exits 10", func(t *testing.T) {
		out, code := run(toolArgs("check", "--plan-dir", planDir)...)
		if code != exitDestructive {
			t.Fatalf("check exit = %d, want %d\n%s", code, exitDestructive, out)
		}
	})
}
