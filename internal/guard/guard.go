// Package guard holds the apply-gate policy: what word the user must type, and
// when tfw refuses to run terraform at all.
//
// Keeping this in its own package means the rules can be tested as data rather
// than by driving the CLI.
package guard

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/ScaferuZ/tfw/internal/plan"
)

// Words are the confirmations the gate accepts.
const (
	WordDestroy = "destroy" // destructive plan; case-sensitive on purpose
	WordYes     = "yes"     // clean plan, same word terraform itself asks for
)

// Request is everything the gate needs to decide what to do.
type Request struct {
	Summary     plan.Summary
	AutoApprove bool // -auto-approve was passed
	Force       bool // --force was passed
}

// Decision is the outcome: prompt for a word, or refuse outright.
type Decision struct {
	Prompt  bool   // ask the user
	Word    string // the literal word that unlocks apply
	Blocked bool   // refuse to run terraform
	Reason  string // human-readable why, filled when Blocked
}

// Decide applies the rules:
//
//	destructive + -auto-approve  -> blocked (never let a CI habit delete infra)
//	destructive                  -> require "destroy"
//	clean                        -> require "yes"
//	clean + --force              -> no prompt
//	destructive + --force        -> no prompt, banner still printed by the caller
func Decide(req Request) Decision {
	destructive := req.Summary.Destructive()
	switch {
	case destructive && req.AutoApprove && !req.Force:
		return Decision{
			Blocked: true,
			Reason:  "refusing -auto-approve on a destructive plan (pass --force if you really mean it)",
		}
	case req.Force:
		return Decision{}
	case destructive:
		return Decision{Prompt: true, Word: WordDestroy}
	default:
		return Decision{Prompt: true, Word: WordYes}
	}
}

// Confirm prints question and reads one line from r, reporting whether it
// matched want exactly. EOF and read errors count as a "no".
func Confirm(w io.Writer, r io.Reader, question, want string) bool {
	fmt.Fprintf(w, "%s ", question)
	line, err := bufio.NewReader(r).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false
	}
	return strings.TrimSpace(line) == want
}
