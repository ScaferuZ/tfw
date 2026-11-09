// Package plan reads a saved terraform plan and classifies its changes.
//
// It knows only what a destroy gate needs: the address of each resource, its
// previous address (moves), whether it is imported, and change.actions.
// Format: https://developer.hashicorp.com/terraform/internals/json-format
package plan

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"

	"github.com/ScaferuZ/tfw/internal/terraform"
)

// Plan is the subset of the plan JSON that tfw reads.
type Plan struct {
	FormatVersion    string           `json:"format_version"`
	TerraformVersion string           `json:"terraform_version"`
	ResourceChanges  []ResourceChange `json:"resource_changes"`
}

// ResourceChange is one address in the plan.
type ResourceChange struct {
	Address         string `json:"address"`
	PreviousAddress string `json:"previous_address"`
	Mode            string `json:"mode"`
	Type            string `json:"type"`
	Name            string `json:"name"`
	Change          Change `json:"change"`
}

// Change holds the actions terraform would take for one resource.
type Change struct {
	Actions   []string        `json:"actions"`
	Before    json.RawMessage `json:"before"`
	After     json.RawMessage `json:"after"`
	Importing *Importing      `json:"importing"`
}

// Importing is set when the change is an import rather than a create.
type Importing struct {
	ID       string          `json:"id"`
	Identity json.RawMessage `json:"identity"`
}

// Summary is the classified view of a plan: addresses grouped by what happens
// to them, each group sorted for stable output.
type Summary struct {
	Destroy []string
	Replace []string
	Update  []string
	Add     []string
	Import  []string
	Moved   []string
}

// Destructive reports whether applying this plan deletes any object.
//
// Replaces count: `["delete","create"]` destroys the old object first, and
// `["create","delete"]` destroys it after the new one exists. Either way
// something is deleted, which is exactly what a destroy guard exists to stop.
func (s Summary) Destructive() bool { return len(s.Destroy) > 0 || len(s.Replace) > 0 }

// Deleted returns every address that leaves the provider, deletes first.
func (s Summary) Deleted() []string {
	out := make([]string, 0, len(s.Destroy)+len(s.Replace))
	out = append(out, s.Destroy...)
	return append(out, s.Replace...)
}

// Load reads a plan file. JSON is parsed directly; a binary tfplan is converted
// with `terraform show -json` first.
func Load(path string) (Plan, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Plan{}, fmt.Errorf("no plan file at %s (run `tfw plan` first)", path)
		}
		return Plan{}, err
	}
	if !json.Valid(data) {
		if data, err = terraform.ShowJSON(path); err != nil {
			return Plan{}, err
		}
	}
	var p Plan
	if err := json.Unmarshal(data, &p); err != nil {
		return Plan{}, fmt.Errorf("parse plan %s: %w", path, err)
	}
	p.ResourceChanges = withoutNoOps(p.ResourceChanges)
	return p, nil
}

// Summarize classifies the plan's changes.
func (p Plan) Summarize() Summary {
	var s Summary
	for _, r := range p.ResourceChanges {
		switch {
		case r.moved():
			s.Moved = append(s.Moved, r.Address)
		case r.Change.imports():
			s.Import = append(s.Import, r.Address)
		case r.Change.replaces():
			s.Replace = append(s.Replace, r.Address)
		case r.Change.has("create"):
			s.Add = append(s.Add, r.Address)
		case r.Change.has("update"):
			s.Update = append(s.Update, r.Address)
		case r.Change.has("delete"):
			s.Destroy = append(s.Destroy, r.Address)
		}
	}
	for _, group := range [][]string{s.Destroy, s.Replace, s.Update, s.Add, s.Import, s.Moved} {
		sort.Strings(group)
	}
	return s
}

func (r ResourceChange) moved() bool {
	return r.PreviousAddress != "" && r.PreviousAddress != r.Address
}

func (c Change) has(action string) bool {
	for _, a := range c.Actions {
		if a == action {
			return true
		}
	}
	return false
}

// replaces reports a two-step change (destroy+create in either order).
func (c Change) replaces() bool { return len(c.Actions) == 2 }

func (c Change) imports() bool {
	return c.Importing != nil && (c.Importing.ID != "" || len(c.Importing.Identity) > 0)
}

// withoutNoOps drops resources that change nothing, keeping moves and imports
// (which appear as no-op yet still matter).
func withoutNoOps(in []ResourceChange) []ResourceChange {
	out := make([]ResourceChange, 0, len(in))
	for _, r := range in {
		noop := len(r.Change.Actions) == 1 && r.Change.Actions[0] == "no-op"
		if noop && !r.moved() && !r.Change.imports() {
			continue
		}
		out = append(out, r)
	}
	return out
}
