package terraform

import (
	"os"
	"testing"
)

func TestBinaryEnvOverride(t *testing.T) {
	t.Setenv("TFW_TERRAFORM", "/fake/terraform")
	t.Setenv("TFW_TERRAGRUNT", "/fake/terragrunt")
	if got := Terraform.Binary(); got != "/fake/terraform" {
		t.Fatalf("terraform binary = %q", got)
	}
	if got := Terragrunt.Binary(); got != "/fake/terragrunt" {
		t.Fatalf("terragrunt binary = %q", got)
	}
	os.Unsetenv("TFW_TERRAFORM")
	os.Unsetenv("TFW_TERRAGRUNT")
	if got := Terraform.Binary(); got != "terraform" {
		t.Fatalf("default terraform binary = %q", got)
	}
	if got := Terragrunt.Binary(); got != "terragrunt" {
		t.Fatalf("default terragrunt binary = %q", got)
	}
	if !Terragrunt.Wraps() || Terraform.Wraps() {
		t.Fatal("only terragrunt wraps terraform")
	}
}

func TestUsesAutoApprove(t *testing.T) {
	yes := [][]string{
		{"-auto-approve"},
		{"--auto-approve"},
		{"-auto-approve=true"},
		{"-var=x", "-auto-approve"},
		{"--non-interactive"},            // terragrunt 1.x global flag
		{"--terragrunt-non-interactive"}, // terragrunt < 1.0 spelling
		{"-var=x", "--non-interactive"},  // anywhere in the list
	}
	no := [][]string{
		{"-var=x"},
		{"-target=a.b"},
		{"-auto-approve=false"}, // still counts as present, see below
	}
	for _, args := range yes {
		if !Terragrunt.UsesAutoApprove(args) {
			t.Fatalf("%v should count as auto-approve", args)
		}
	}
	for _, args := range no[:2] {
		if Terragrunt.UsesAutoApprove(args) {
			t.Fatalf("%v should not count as auto-approve", args)
		}
	}
	// -auto-approve=false is refused rather than trusted: a plan that says
	// "no" here is not worth the risk of guessing.
	if !Terraform.UsesAutoApprove(no[2]) {
		t.Fatal("-auto-approve=... should count as auto-approve")
	}
}

func TestOutFlag(t *testing.T) {
	cases := []struct {
		args []string
		has  bool
		val  string
	}{
		{[]string{"-out=x.tfplan"}, true, "x.tfplan"},
		{[]string{"--out", "x.tfplan"}, true, "x.tfplan"},
		{[]string{"-var=a=b"}, false, ""},
	}
	for _, c := range cases {
		if got := HasOutFlag(c.args); got != c.has {
			t.Fatalf("HasOutFlag(%v) = %v", c.args, got)
		}
		if got := OutFlagValue(c.args); got != c.val {
			t.Fatalf("OutFlagValue(%v) = %q", c.args, got)
		}
	}
}
