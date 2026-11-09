// Package terraform wraps the terraform CLI binary: locating it, running it
// with the caller's stdio attached, and converting a binary plan file into the
// JSON that the rest of tfw reads.
package terraform

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Binary is the terraform executable tfw drives. TFW_TERRAFORM overrides the
// lookup, which is what the tests use to substitute a fake.
func Binary() string {
	if v := os.Getenv("TFW_TERRAFORM"); v != "" {
		return v
	}
	return "terraform"
}

// Run executes terraform with stdin/stdout/stderr attached and returns its
// exit code, so callers can pass it straight through.
func Run(args ...string) int {
	cmd := exec.Command(Binary(), args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return ExitCode(cmd.Run())
}

// ShowJSON renders a binary plan file as JSON. Terraform owns the plan file
// format; shelling out to `terraform show -json` is the only supported way to
// read it, and it needs the same terraform version that wrote the file.
func ShowJSON(path string) ([]byte, error) {
	cmd := exec.Command(Binary(), "show", "-json", path)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("terraform show -json %s: %s", path, msg)
	}
	return out, nil
}

// UsesAutoApprove reports whether the argument list asks terraform to skip its
// own confirmation.
func UsesAutoApprove(args []string) bool {
	for _, a := range args {
		if a == "-auto-approve" || a == "--auto-approve" || strings.HasPrefix(a, "-auto-approve=") {
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
