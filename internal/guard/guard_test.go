package guard

import (
	"strings"
	"testing"

	"github.com/ScaferuZ/tfw/internal/plan"
)

func destructive() plan.Summary {
	return plan.Summary{Destroy: []string{"aws_db_instance.legacy"}}
}

func clean() plan.Summary {
	return plan.Summary{Add: []string{"aws_instance.web"}}
}

func TestDecide(t *testing.T) {
	cases := []struct {
		name string
		req  Request
		want Decision
	}{
		{
			name: "clean plan asks for yes",
			req:  Request{Summary: clean()},
			want: Decision{Prompt: true, Word: WordYes},
		},
		{
			name: "destructive plan asks for destroy",
			req:  Request{Summary: destructive()},
			want: Decision{Prompt: true, Word: WordDestroy},
		},
		{
			name: "auto-approve on destructive plan is blocked",
			req:  Request{Summary: destructive(), AutoApprove: true},
			want: Decision{Blocked: true, Reason: "refusing -auto-approve on a destructive plan (pass --force if you really mean it)"},
		},
		{
			name: "auto-approve on clean plan is fine",
			req:  Request{Summary: clean(), AutoApprove: true},
			want: Decision{Prompt: true, Word: WordYes},
		},
		{
			name: "force skips the prompt on a destructive plan",
			req:  Request{Summary: destructive(), Force: true},
			want: Decision{},
		},
		{
			name: "force overrides the auto-approve block",
			req:  Request{Summary: destructive(), AutoApprove: true, Force: true},
			want: Decision{},
		},
		{
			name: "force skips the prompt on a clean plan",
			req:  Request{Summary: clean(), Force: true},
			want: Decision{},
		},
		{
			name: "replaces count as destructive",
			req:  Request{Summary: plan.Summary{Replace: []string{"aws_instance.bastion"}}},
			want: Decision{Prompt: true, Word: WordDestroy},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Decide(tc.req)
			if got != tc.want {
				t.Fatalf("Decide() = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestConfirm(t *testing.T) {
	cases := []struct {
		input string
		want  string
		ok    bool
	}{
		{"destroy\n", WordDestroy, true},
		{"destroy", WordDestroy, true},    // no trailing newline
		{"yes\n", WordDestroy, false},     // wrong word
		{"DESTROY\n", WordDestroy, false}, // case-sensitive
		{"", WordDestroy, false},          // EOF
		{"  destroy  \n", WordDestroy, true},
		{"yes\n", WordYes, true},
	}
	for _, tc := range cases {
		var out strings.Builder
		if got := Confirm(&out, strings.NewReader(tc.input), "question:", tc.want); got != tc.ok {
			t.Errorf("Confirm(%q, want %q) = %v, want %v", tc.input, tc.want, got, tc.ok)
		}
		if !strings.Contains(out.String(), "question:") {
			t.Errorf("prompt was not printed for %q", tc.input)
		}
	}
}
