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

// TestApplyGate drives the built binary against a fake `terraform` so we can
// prove a destructive plan never reaches apply on the wrong word.
func TestApplyGate(t *testing.T) {
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

	if out, code := run("plan", "--plan-dir", planDir); code != 0 {
		t.Fatalf("plan: exit %d\n%s", code, out)
	}

	t.Run("yes is rejected", func(t *testing.T) {
		out, code := withInput("yes\n", "apply", "--plan-dir", planDir)
		if code == 0 {
			t.Fatalf("apply accepted 'yes' on a destructive plan\n%s", out)
		}
		if strings.Contains(out, "SHIM-TERRAFORM-APPLY") {
			t.Fatalf("terraform apply was reached with 'yes'\n%s", out)
		}
	})

	t.Run("uppercase DESTROY rejected", func(t *testing.T) {
		out, code := withInput("DESTROY\n", "apply", "--plan-dir", planDir)
		if code == 0 || strings.Contains(out, "SHIM-TERRAFORM-APPLY") {
			t.Fatalf("apply accepted 'DESTROY'\n%s", out)
		}
	})

	t.Run("auto-approve blocked", func(t *testing.T) {
		out, code := run("apply", "--plan-dir", planDir, "-auto-approve")
		if code == 0 || strings.Contains(out, "SHIM-TERRAFORM-APPLY") {
			t.Fatalf("-auto-approve was not blocked\n%s", out)
		}
		if !strings.Contains(out, "BLOCKED") {
			t.Fatalf("expected BLOCKED message\n%s", out)
		}
	})

	t.Run("destroy is accepted", func(t *testing.T) {
		out, code := withInput("destroy\n", "apply", "--plan-dir", planDir)
		if code != 0 {
			t.Fatalf("apply with 'destroy' failed: exit %d\n%s", code, out)
		}
		if !strings.Contains(out, "SHIM-TERRAFORM-APPLY") {
			t.Fatalf("terraform apply was not reached\n%s", out)
		}
	})

	t.Run("check exits 10", func(t *testing.T) {
		out, code := run("check", "--plan-dir", planDir)
		if code != exitDestructive {
			t.Fatalf("check exit = %d, want %d\n%s", code, exitDestructive, out)
		}
	})
}
