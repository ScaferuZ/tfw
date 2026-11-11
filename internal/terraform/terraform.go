// Package terraform wraps the CLI binary tfw drives — terraform itself, or
// terragrunt, which runs terraform for you in a per-module working directory.
// It locates the binary, runs it with the caller's stdio attached, and converts
// a binary plan file into the JSON that the rest of tfw reads.
package terraform

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Tool is one of the CLIs that can produce and apply a plan.
//
// Both accept the same subcommands and pass terraform's flags through, so tfw
// only needs to know which binary to exec and which environment variable
// overrides it.
type Tool struct {
	Name   string // "terraform" or "terragrunt"
	EnvVar string // environment variable that overrides the binary path
}

// The tools tfw knows how to drive.
var (
	Terraform  = Tool{Name: "terraform", EnvVar: "TFW_TERRAFORM"}
	Terragrunt = Tool{Name: "terragrunt", EnvVar: "TFW_TERRAGRUNT"}
)

// Binary is the executable tfw drives. The tool's environment variable
// overrides the lookup, which is what the tests use to substitute a fake.
func (t Tool) Binary() string {
	if v := os.Getenv(t.EnvVar); v != "" {
		return v
	}
	return t.Name
}

// Wraps reports whether the tool runs terraform for us in a working directory
// of its own rather than in the current one.
func (t Tool) Wraps() bool { return t.Name == Terragrunt.Name }

// Run executes the tool with stdin/stdout/stderr attached and returns its exit
// code, so callers can pass it straight through.
func (t Tool) Run(args ...string) int {
	cmd := exec.Command(t.Binary(), args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	err := cmd.Run()
	if errors.Is(err, exec.ErrNotFound) {
		fmt.Fprintf(os.Stderr, "tfw: %s not found in PATH (install it, or set %s=/path/to/%s)\n",
			t.Name, t.EnvVar, t.Name)
		return 1
	}
	return ExitCode(err)
}

// ShowJSON renders a binary plan file as JSON. Terraform owns the plan file
// format; shelling out to `show -json` is the only supported way to read it,
// and it needs the same binary version that wrote the file. Terragrunt runs
// terraform's show inside the module directory, which is why the path is
// resolved by the caller.
func (t Tool) ShowJSON(path string) ([]byte, error) {
	cmd := exec.Command(t.Binary(), "show", "-json", path)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("%s show -json %s: %s", t.Name, path, msg)
	}
	return out, nil
}

// UsesAutoApprove reports whether the argument list asks the tool to skip its
// own confirmation. Terragrunt's --non-interactive ("assume yes for all
// prompts") counts: it is -auto-approve wearing a different name.
func (t Tool) UsesAutoApprove(args []string) bool {
	for _, a := range args {
		switch {
		case a == "-auto-approve", a == "--auto-approve", strings.HasPrefix(a, "-auto-approve="):
			return true
		case a == "-non-interactive", a == "--non-interactive",
			a == "-terragrunt-non-interactive", a == "--terragrunt-non-interactive",
			strings.HasPrefix(a, "-non-interactive="), strings.HasPrefix(a, "--non-interactive="):
			return true
		}
	}
	return false
}

// HasOutFlag reports whether the caller already supplied -out.
func HasOutFlag(args []string) bool {
	for _, a := range args {
		if a == "-out" || a == "--out" || strings.HasPrefix(a, "-out=") || strings.HasPrefix(a, "--out=") {
			return true
		}
	}
	return false
}

// OutFlagValue returns the path given to -out, or "".
func OutFlagValue(args []string) string {
	for i, a := range args {
		switch {
		case a == "-out" || a == "--out":
			if i+1 < len(args) {
				return args[i+1]
			}
		case strings.HasPrefix(a, "-out="):
			return strings.TrimPrefix(a, "-out=")
		case strings.HasPrefix(a, "--out="):
			return strings.TrimPrefix(a, "--out=")
		}
	}
	return ""
}

// ExitCode maps an exec error to a process exit code.
func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return 1
}
