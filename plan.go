package main

// Own plan model and renderer: no dependency on tf-summarize or
// hashicorp/terraform-json. Only stdlib.
//
// The plan JSON is the only contract we need:
// https://developer.hashicorp.com/terraform/internals/json-format
// Four fields matter for a destroy gate: address, previous_address,
// change.actions, change.importing.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
)

type planFile struct {
	FormatVersion    string           `json:"format_version"`
	TerraformVersion string           `json:"terraform_version"`
	ResourceChanges  []resourceChange `json:"resource_changes"`
}

type resourceChange struct {
	Address         string `json:"address"`
	PreviousAddress string `json:"previous_address"`
	Mode            string `json:"mode"`
	Type            string `json:"type"`
	Name            string `json:"name"`
	Change          change `json:"change"`
}

type change struct {
	Actions   []string        `json:"actions"`
	Before    json.RawMessage `json:"before"`
	After     json.RawMessage `json:"after"`
	Importing *importing      `json:"importing"`
}

type importing struct {
	ID       string          `json:"id"`
	Identity json.RawMessage `json:"identity"`
}

func (c change) has(action string) bool {
	for _, a := range c.Actions {
		if a == action {
			return true
		}
	}
	return false
}

// deletes reports whether the apply removes an object from the provider:
// plain destroy, or a replace in either order (destroy-then-create and
// create-before-destroy both delete the old object).
func (c change) deletes() bool { return c.has("delete") }

// replaces reports a two-step change (destroy+create in some order).
func (c change) replaces() bool { return len(c.Actions) == 2 }

func (c change) imports() bool {
	return c.Importing != nil && (c.Importing.ID != "" || len(c.Importing.Identity) > 0)
}

// loadPlan reads a plan file: JSON as-is, or a binary tfplan converted with
// `terraform show -json`.
func loadPlan(path string) (planFile, error) {
	var p planFile
	if _, err := os.Stat(path); err != nil {
		return p, fmt.Errorf("no plan file at %s (run `tfw plan` first)", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return p, err
	}
	if !json.Valid(data) {
		if data, err = planToJSON(path); err != nil {
			return p, err
		}
	}
	if err := json.Unmarshal(data, &p); err != nil {
		return p, fmt.Errorf("parse plan %s: %w", path, err)
	}
	p.ResourceChanges = withoutNoOps(p.ResourceChanges)
	return p, nil
}

func planToJSON(path string) ([]byte, error) {
	cmd := exec.Command(terraformBinary(), "show", "-json", path)
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

// withoutNoOps drops resources that change nothing, but keeps moves and
// imports (they appear as no-op in the plan yet still matter).
func withoutNoOps(in []resourceChange) []resourceChange {
	out := make([]resourceChange, 0, len(in))
	for _, r := range in {
		noop := len(r.Change.Actions) == 1 && r.Change.Actions[0] == "no-op"
		moved := r.PreviousAddress != "" && r.PreviousAddress != r.Address
		if noop && !moved && !r.Change.imports() {
			continue
		}
		out = append(out, r)
	}
	return out
}

type buckets struct {
	Destroy, Replace, Update, Add, Import, Moved []string
}

func classify(p planFile) buckets {
	var b buckets
	for _, r := range p.ResourceChanges {
		moved := r.PreviousAddress != "" && r.PreviousAddress != r.Address
		switch {
		case moved:
			b.Moved = append(b.Moved, r.Address)
		case r.Change.imports():
			b.Import = append(b.Import, r.Address)
		case r.Change.replaces():
			b.Replace = append(b.Replace, r.Address)
		case r.Change.has("create"):
			b.Add = append(b.Add, r.Address)
		case r.Change.has("update"):
			b.Update = append(b.Update, r.Address)
		case r.Change.has("delete"):
			b.Destroy = append(b.Destroy, r.Address)
		}
	}
	for _, s := range [][]string{b.Destroy, b.Replace, b.Update, b.Add, b.Import, b.Moved} {
		sort.Strings(s)
	}
	return b
}

// summary prints the plan table, most dangerous rows first.
func summary(s style, w io.Writer, p planFile) error {
	b := classify(p)
	type row struct {
		label string
		color string
		addrs []string
	}
	rows := []row{
		{"destroy", ansiRed, b.Destroy},
		{"replace", ansiRed, b.Replace},
		{"update", ansiYellow, b.Update},
		{"add", ansiGreen, b.Add},
		{"import", ansiCyan, b.Import},
		{"moved", ansiCyan, b.Moved},
	}

	var sb strings.Builder
	sb.WriteString(s.paint(ansiBold, pad("CHANGE", 14)) + s.paint(ansiBold, "RESOURCE") + "\n")
	for _, r := range rows {
		if len(r.addrs) == 0 {
			continue
		}
		for i, a := range r.addrs {
			label := ""
			if i == 0 {
				label = fmt.Sprintf("%s (%d)", r.label, len(r.addrs))
			}
			sb.WriteString(s.paint(r.color, pad(label, 14)) + a + "\n")
		}
	}
	_, err := io.WriteString(w, sb.String())
	return err
}
