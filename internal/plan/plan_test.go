package plan

import (
	"strings"
	"testing"
)

func load(t *testing.T, name string) Plan {
	t.Helper()
	p, err := Load("../../testdata/" + name)
	if err != nil {
		t.Fatalf("Load(%s): %v", name, err)
	}
	return p
}

func TestMixedPlan(t *testing.T) {
	sum := load(t, "mixed.json").Summarize()

	if got, want := len(sum.Add), 2; got != want {
		t.Errorf("add = %d, want %d", got, want)
	}
	if got, want := len(sum.Update), 1; got != want {
		t.Errorf("update = %d, want %d", got, want)
	}
	if got, want := len(sum.Destroy), 1; got != want {
		t.Errorf("destroy = %d, want %d", got, want)
	}
	if got, want := len(sum.Replace), 1; got != want {
		t.Errorf("replace = %d, want %d", got, want)
	}
	if !sum.Destructive() {
		t.Error("mixed plan must be destructive")
	}
	if len(sum.Destroy) > 0 && sum.Destroy[0] != "aws_db_instance.legacy" {
		t.Errorf("destroy = %v", sum.Destroy)
	}
	if len(sum.Replace) > 0 && sum.Replace[0] != "aws_instance.bastion" {
		t.Errorf("replace = %v", sum.Replace)
	}
	// no-op resources are dropped, so the totals match the 5 real changes
	total := len(sum.Add) + len(sum.Update) + len(sum.Destroy) + len(sum.Replace)
	if total != 5 {
		t.Errorf("total changes = %d, want 5 (no-op must be filtered)", total)
	}
}

func TestSafePlan(t *testing.T) {
	sum := load(t, "safe.json").Summarize()
	if sum.Destructive() {
		t.Fatalf("safe plan flagged destructive: %+v", sum)
	}
	if len(sum.Add) != 1 || len(sum.Update) != 1 {
		t.Fatalf("summary = %+v", sum)
	}
}

func TestDeletedMergesDestroyAndReplace(t *testing.T) {
	sum := load(t, "mixed.json").Summarize()
	got := strings.Join(sum.Deleted(), ",")
	if got != "aws_db_instance.legacy,aws_instance.bastion" {
		t.Fatalf("Deleted() = %q", got)
	}
}

func TestActionClassification(t *testing.T) {
	cases := []struct {
		name    string
		actions []string
		want    string
	}{
		{"create", []string{"create"}, "add"},
		{"update", []string{"update"}, "update"},
		{"delete", []string{"delete"}, "destroy"},
		{"replace destroy-first", []string{"delete", "create"}, "replace"},
		{"replace create-first", []string{"create", "delete"}, "replace"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := Plan{ResourceChanges: []ResourceChange{{
				Address: "a.b",
				Change:  Change{Actions: tc.actions},
			}}}
			sum := p.Summarize()
			got := ""
			switch {
			case len(sum.Add) == 1:
				got = "add"
			case len(sum.Update) == 1:
				got = "update"
			case len(sum.Destroy) == 1:
				got = "destroy"
			case len(sum.Replace) == 1:
				got = "replace"
			}
			if got != tc.want {
				t.Fatalf("actions %v classified as %q, want %q", tc.actions, got, tc.want)
			}
		})
	}
}

func TestMovedAndImportedAreNotDestructive(t *testing.T) {
	p := Plan{ResourceChanges: []ResourceChange{
		{Address: "aws_instance.new", PreviousAddress: "aws_instance.old",
			Change: Change{Actions: []string{"no-op"}}},
		{Address: "aws_s3_bucket.imported",
			Change: Change{Actions: []string{"no-op"}, Importing: &Importing{ID: "bucket-1"}}},
	}}
	sum := p.Summarize()
	if sum.Destructive() {
		t.Fatalf("move/import flagged destructive: %+v", sum)
	}
	if len(sum.Moved) != 1 || len(sum.Import) != 1 {
		t.Fatalf("summary = %+v", sum)
	}
}
