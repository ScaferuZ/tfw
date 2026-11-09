package render

import (
	"strings"
	"testing"

	"github.com/ScaferuZ/tfw/internal/plan"
)

var mixed = plan.Summary{
	Destroy: []string{"aws_db_instance.legacy"},
	Replace: []string{"aws_instance.bastion"},
	Update:  []string{"aws_security_group.web"},
	Add:     []string{"aws_instance.web", "aws_s3_bucket.assets"},
}

// Destructive rows must come first: a guard that makes you scroll to find the
// destroy is not a guard.
func TestTablePutsDestroysFirst(t *testing.T) {
	var out strings.Builder
	if err := (Style{}).Table(&out, mixed); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) != 6 {
		t.Fatalf("expected header + 5 rows, got %d lines:\n%s", len(lines), out.String())
	}
	if !strings.HasPrefix(lines[0], "CHANGE") || !strings.Contains(lines[0], "RESOURCE") {
		t.Fatalf("bad header: %q", lines[0])
	}
	if !strings.Contains(lines[1], "destroy (1)") || !strings.Contains(lines[1], "aws_db_instance.legacy") {
		t.Fatalf("destroy not first: %q", lines[1])
	}
	if !strings.Contains(lines[2], "replace (1)") {
		t.Fatalf("replace not second: %q", lines[2])
	}
	// second address in a group has an empty label column
	if !strings.HasSuffix(strings.TrimSpace(lines[5]), "aws_s3_bucket.assets") {
		t.Fatalf("last row = %q", lines[5])
	}
}

func TestTableSkipsEmptyGroups(t *testing.T) {
	var out strings.Builder
	if err := (Style{}).Table(&out, plan.Summary{Add: []string{"a.b"}}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "destroy") || strings.Contains(out.String(), "update") {
		t.Fatalf("empty groups printed:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "add (1)") {
		t.Fatalf("add row missing:\n%s", out.String())
	}
}

func TestCountsLine(t *testing.T) {
	var out strings.Builder
	Style{}.Counts(&out, mixed)
	got := out.String()
	for _, want := range []string{"Plan:", "+2 add", "~1 change", "-1 destroy", "-/+1 replace"} {
		if !strings.Contains(got, want) {
			t.Errorf("counts line %q missing %q", got, want)
		}
	}
}

func TestCountsNoChanges(t *testing.T) {
	var out strings.Builder
	Style{}.Counts(&out, plan.Summary{})
	if !strings.Contains(out.String(), "No changes") {
		t.Fatalf("got %q", out.String())
	}
}

func TestBannerListsEveryAddress(t *testing.T) {
	var out strings.Builder
	Style{}.Banner(&out, mixed, ".tfw/tfw.tfplan")
	got := out.String()
	for _, addr := range append(append([]string{}, mixed.Destroy...), mixed.Replace...) {
		if !strings.Contains(got, addr) {
			t.Errorf("banner missing %q:\n%s", addr, got)
		}
	}
	if !strings.Contains(got, "D E S T R O Y") || !strings.Contains(got, `"destroy"`) {
		t.Errorf("banner missing the ask:\n%s", got)
	}
}

func TestStylePaint(t *testing.T) {
	if got := (Style{}).Paint(Red, "x"); got != "x" {
		t.Errorf("disabled style painted: %q", got)
	}
	if got := (Style{Enabled: true}).Paint(Red, "x"); !strings.Contains(got, Red) || !strings.Contains(got, Reset) {
		t.Errorf("enabled style did not paint: %q", got)
	}
	if got := (Style{Enabled: true}).Paint(Red, ""); got != "" {
		t.Errorf("empty text should stay empty, got %q", got)
	}
}
