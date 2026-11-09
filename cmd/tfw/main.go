// tfw is a thin guard around terraform plan/apply.
//
// It reuses tf-summarize's own reader/parser/writer packages so the plan
// summary looks exactly like `tf-summarize`, and adds one thing tf-summarize
// deliberately does not do: it sits in front of `terraform apply` and refuses
// to continue unless you type the word "destroy" when the plan deletes or
// replaces resources.
package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/dineshba/tf-summarize/parser"
	"github.com/dineshba/tf-summarize/reader"
	"github.com/dineshba/tf-summarize/terraformstate"
	"github.com/dineshba/tf-summarize/writer"
	tfjson "github.com/hashicorp/terraform-json"
)

const version = "0.1.0"

// exitDestructive is returned by `tfw check` when the plan deletes or replaces
// anything, so CI can gate on it: `tfw check plan.json || exit 1`.
const exitDestructive = 10

const bannerWidth = 76

// ---------------------------------------------------------------- styling --

const (
	ansiReset  = "\033[0m"
	ansiRed    = "\033[1;31m"
	ansiGreen  = "\033[1;32m"
	ansiYellow = "\033[1;33m"
	ansiCyan   = "\033[1;36m"
	ansiBold   = "\033[1m"
	ansiDim    = "\033[2m"
	ansiBanner = "\033[1;97;41m" // bold white on red
)

type style struct{ enabled bool }

func (s style) paint(code, text string) string {
	if !s.enabled {
		return text
	}
	return code + text + ansiReset
}

func isTTY(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// newStyle resolves --color / NO_COLOR / tty detection.
func newStyle(mode string) style {
	switch mode {
	case "always":
		return style{true}
	case "never":
		return style{false}
	}
	if os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		return style{false}
	}
	return style{isTTY(os.Stdout)}
}

// ------------------------------------------------------------------ flags --

type opts struct {
	colorMode string
	planDir   string
	force     bool
}

func defaultOpts() *opts { return &opts{colorMode: "auto", planDir: ".tfw"} }

// extractTfwFlags pulls tfw's own flags out of args wherever they appear and
// returns everything else untouched, so unknown flags reach terraform verbatim
// (`tfw plan -var-file=x.tfvars -out=tfplan` just works). A bare "--" stops
// tfw from interpreting anything after it.
func extractTfwFlags(args []string, o *opts) []string {
	rest := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			return append(rest, args[i+1:]...)
		case a == "--force" || a == "-force":
			o.force = true
		case a == "--color" || a == "-color":
			if i+1 < len(args) {
				o.colorMode = args[i+1]
				i++
			}
		case strings.HasPrefix(a, "--color=") || strings.HasPrefix(a, "-color="):
			o.colorMode = strings.SplitN(a, "=", 2)[1]
		case a == "--plan-dir" || a == "-plan-dir":
			if i+1 < len(args) {
				o.planDir = args[i+1]
				i++
			}
		case strings.HasPrefix(a, "--plan-dir=") || strings.HasPrefix(a, "-plan-dir="):
			o.planDir = strings.SplitN(a, "=", 2)[1]
		default:
			rest = append(rest, a)
		}
	}
	return rest
}

// ------------------------------------------------------------- plan input --

func loadPlan(path string) (tfjson.Plan, error) {
	if _, err := os.Stat(path); err != nil {
		return tfjson.Plan{}, fmt.Errorf("no plan file at %s (run `tfw plan` first)", path)
	}
	r, err := reader.CreateReader([]string{path})
	if err != nil {
		return tfjson.Plan{}, err
	}
	data, err := r.Read()
	if err != nil {
		return tfjson.Plan{}, err
	}
	p, err := parser.CreateParser(data, r.Name())
	if err != nil {
		return tfjson.Plan{}, err
	}
	plan, err := p.Parse()
	if err != nil {
		return tfjson.Plan{}, err
	}
	terraformstate.FilterNoOpResources(&plan)
	return plan, nil
}

type counts struct {
	Add, Change, Delete, Replace, Import, Moved int
}

func summarize(plan tfjson.Plan) counts {
	m := terraformstate.GetAllResourceChanges(plan)
	return counts{
		Add:     len(m["add"]),
		Change:  len(m["update"]),
		Delete:  len(m["delete"]),
		Replace: len(m["recreate"]),
		Import:  len(m["import"]),
		Moved:   len(m["moved"]),
	}
}

func (c counts) destructive() bool { return c.Delete > 0 || c.Replace > 0 }

// destroyedLists returns the addresses that will vanish from state, either
// fully deleted or destroyed-and-recreated.
func destroyedLists(plan tfjson.Plan) (deleted, replaced []string) {
	m := terraformstate.GetAllResourceChanges(plan)
	for _, r := range m["delete"] {
		deleted = append(deleted, r.Address)
	}
	for _, r := range m["recreate"] {
		replaced = append(replaced, r.Address)
	}
	return deleted, replaced
}

func printSummary(w io.Writer, plan tfjson.Plan) error {
	wr := writer.CreateWriter(false, false, false, false, false, false, false, plan)
	return wr.Write(w)
}

func printCounts(s style, c counts) {
	var parts []string
	if c.Add > 0 {
		parts = append(parts, s.paint(ansiGreen, fmt.Sprintf("+%d add", c.Add)))
	}
	if c.Change > 0 {
		parts = append(parts, s.paint(ansiYellow, fmt.Sprintf("~%d change", c.Change)))
	}
	if c.Delete > 0 {
		parts = append(parts, s.paint(ansiRed, fmt.Sprintf("-%d destroy", c.Delete)))
	}
	if c.Replace > 0 {
		parts = append(parts, s.paint(ansiRed, fmt.Sprintf("-/+%d replace", c.Replace)))
	}
	if c.Import > 0 {
		parts = append(parts, s.paint(ansiCyan, fmt.Sprintf("i%d import", c.Import)))
	}
	if c.Moved > 0 {
		parts = append(parts, s.paint(ansiCyan, fmt.Sprintf("->%d moved", c.Moved)))
	}
	if len(parts) == 0 {
		fmt.Printf("%s No changes. Your infrastructure matches the configuration.\n", s.paint(ansiGreen, "OK"))
		return
	}
	fmt.Printf("%s %s\n", s.paint(ansiBold, "Plan:"), strings.Join(parts, s.paint(ansiDim, "  ")))
}

// ---------------------------------------------------------------- banners --

func pad(text string, width int) string {
	if len([]rune(text)) >= width {
		return text
	}
	return text + strings.Repeat(" ", width-len([]rune(text)))
}

// printBanner prints the big red block. It is printed even when stdout is not
// a tty (CI logs) — just without ANSI color.
func printBanner(s style, c counts, deleted, replaced []string, planPath string) {
	inner := bannerWidth - 5 // "██ " + inner + "██"
	rule := strings.Repeat("█", bannerWidth)

	rows := []string{
		"",
		"  !!  D E S T R O Y  !!",
		"",
	}
	if c.Delete > 0 {
		rows = append(rows, fmt.Sprintf("  %d resource(s) will be DELETED", c.Delete))
	}
	if c.Replace > 0 {
		rows = append(rows, fmt.Sprintf("  %d resource(s) will be REPLACED (destroy + create)", c.Replace))
	}
	rows = append(rows, "")
	for _, a := range append(append([]string{}, deleted...), replaced...) {
		addr := a
		if len([]rune(addr)) > inner-6 {
			addr = string([]rune(addr)[:inner-9]) + "..."
		}
		rows = append(rows, "  x "+addr)
	}
	rows = append(rows, "", `  apply requires typing the word "destroy"`)

	var b strings.Builder
	b.WriteString("\n")
	b.WriteString(s.paint(ansiBanner, rule) + "\n")
	for _, r := range rows {
		b.WriteString(s.paint(ansiBanner, "██ "+pad(r, inner)+"██") + "\n")
	}
	b.WriteString(s.paint(ansiBanner, rule) + "\n")
	fmt.Print(b.String())
	fmt.Printf("%s plan file: %s\n", s.paint(ansiDim, "  "), planPath)
}

// ------------------------------------------------------------------ input --

// confirm reads one line from stdin and compares it to want.
func confirm(question, want string) bool {
	fmt.Printf("%s ", question)
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false
	}
	return strings.TrimSpace(line) == want
}

// -------------------------------------------------------------- terraform --

func terraformBinary() string {
	if v := os.Getenv("TFW_TERRAFORM"); v != "" {
		return v
	}
	return "terraform"
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return 1
}

func runTerraform(args []string) int {
	cmd := exec.Command(terraformBinary(), args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return exitCode(cmd.Run())
}

func hasAutoApprove(args []string) bool {
	for _, a := range args {
		if a == "-auto-approve" || a == "--auto-approve" || strings.HasPrefix(a, "-auto-approve=") {
			return true
		}
	}
	return false
}

// ------------------------------------------------------------------- cmds --

func cmdPlan(raw []string) int {
	o := defaultOpts()
	rest := extractTfwFlags(raw, o)
	s := newStyle(o.colorMode)

	planPath := filepath.Join(o.planDir, "tfw.tfplan")
	if err := os.MkdirAll(o.planDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "tfw: %v\n", err)
		return 1
	}

	tfArgs := append([]string{"plan"}, rest...)
	if !hasOutFlag(rest) {
		tfArgs = append(tfArgs, "-out="+planPath)
	} else {
		planPath = outFlagValue(rest)
	}

	code := runTerraform(tfArgs)
	if code != 0 && code != 2 { // 2 = -detailed-exitcode: changes present
		return code
	}

	plan, err := loadPlan(planPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tfw: %v\n", err)
		return 1
	}
	c := summarize(plan)
	fmt.Println()
	if err := printSummary(os.Stdout, plan); err != nil {
		fmt.Fprintf(os.Stderr, "tfw: %v\n", err)
		return 1
	}
	fmt.Println()
	printCounts(s, c)

	if c.destructive() {
		deleted, replaced := destroyedLists(plan)
		printBanner(s, c, deleted, replaced, planPath)
	}
	fmt.Printf("\nNext: %s\n", s.paint(ansiBold, "tfw apply"))
	return 0
}

func cmdApply(raw []string) int {
	o := defaultOpts()
	rest := extractTfwFlags(raw, o)
	s := newStyle(o.colorMode)

	planPath := filepath.Join(o.planDir, "tfw.tfplan")
	if v, ok := positionalPlan(rest); ok {
		planPath = v
	}

	plan, err := loadPlan(planPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tfw: %v\n", err)
		return 1
	}
	if fi, err := os.Stat(planPath); err == nil {
		if age := time.Since(fi.ModTime()); age > 30*time.Minute {
			fmt.Printf("%s plan file is %s old - re-run `tfw plan` if the config changed\n",
				s.paint(ansiYellow, "WARN"), age.Round(time.Minute))
		}
	}
	c := summarize(plan)
	fmt.Println()
	if err := printSummary(os.Stdout, plan); err != nil {
		fmt.Fprintf(os.Stderr, "tfw: %v\n", err)
		return 1
	}
	fmt.Println()
	printCounts(s, c)

	if !c.destructive() {
		if o.force {
			return runTerraform(append([]string{"apply", planPath}, rest...))
		}
		fmt.Println()
		if !confirm("Apply this plan? Only 'yes' will be accepted:", "yes") {
			fmt.Println(s.paint(ansiRed, "Apply cancelled."))
			return 1
		}
		return runTerraform(append([]string{"apply", planPath}, rest...))
	}

	deleted, replaced := destroyedLists(plan)
	printBanner(s, c, deleted, replaced, planPath)

	if hasAutoApprove(rest) && !o.force {
		fmt.Fprintf(os.Stderr, "\n%s tfw: refusing -auto-approve on a destructive plan (pass --force if you really mean it)\n",
			s.paint(ansiRed, "BLOCKED"))
		return 1
	}
	if !o.force {
		fmt.Println()
		if !confirm(s.paint(ansiRed, `Type "destroy" to apply this plan:`), "destroy") {
			fmt.Println(s.paint(ansiRed, "Apply cancelled. Nothing was changed."))
			return 1
		}
	}
	return runTerraform(append([]string{"apply", planPath}, rest...))
}

func cmdCheck(raw []string) int {
	o := defaultOpts()
	args := extractTfwFlags(raw, o)
	s := newStyle(o.colorMode)
	if len(args) == 0 {
		args = []string{filepath.Join(o.planDir, "tfw.tfplan")}
	}
	plan, err := loadPlan(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "tfw: %v\n", err)
		return 1
	}
	c := summarize(plan)
	printCounts(s, c)
	if c.destructive() {
		deleted, replaced := destroyedLists(plan)
		for _, a := range deleted {
			fmt.Println(s.paint(ansiRed, "  - "+a))
		}
		for _, a := range replaced {
			fmt.Println(s.paint(ansiRed, "  -/+ "+a))
		}
		return exitDestructive
	}
	return 0
}

func cmdSummary(raw []string) int {
	o := defaultOpts()
	args := extractTfwFlags(raw, o)
	if len(args) == 0 {
		args = []string{filepath.Join(o.planDir, "tfw.tfplan")}
	}
	plan, err := loadPlan(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "tfw: %v\n", err)
		return 1
	}
	if err := printSummary(os.Stdout, plan); err != nil {
		fmt.Fprintf(os.Stderr, "tfw: %v\n", err)
		return 1
	}
	return 0
}

// ------------------------------------------------------------------ utils --

func hasOutFlag(args []string) bool {
	for i, a := range args {
		if a == "-out" || a == "--out" || strings.HasPrefix(a, "-out=") || strings.HasPrefix(a, "--out=") {
			_ = i
			return true
		}
	}
	return false
}

func outFlagValue(args []string) string {
	for i, a := range args {
		if a == "-out" || a == "--out" {
			if i+1 < len(args) {
				return args[i+1]
			}
			return ""
		}
		if strings.HasPrefix(a, "-out=") {
			return strings.TrimPrefix(a, "-out=")
		}
		if strings.HasPrefix(a, "--out=") {
			return strings.TrimPrefix(a, "--out=")
		}
	}
	return ""
}

func positionalPlan(args []string) (string, bool) {
	for _, a := range args {
		if !strings.HasPrefix(a, "-") && (strings.HasSuffix(a, ".tfplan") || strings.HasSuffix(a, "plan")) {
			return a, true
		}
	}
	return "", false
}

func usage() {
	fmt.Fprintf(os.Stderr, `tfw %s - terraform plan/apply guard

Usage:
  tfw plan  [terraform plan flags...]   run plan, save .tfw/tfw.tfplan, show summary + DESTROY banner
  tfw apply [terraform apply flags...]  show saved plan, then require "destroy" (or "yes") before applying
  tfw check [plan.json|tfplan]          print counts; exit %d if anything is destroyed/replaced (CI gate)
  tfw summary [plan.json|tfplan]        tf-summarize table view of a plan file
  tfw version

Common flags:
  --plan-dir string   where the saved plan lives (default ".tfw")
  --color string      auto|always|never (default "auto")
  --force             skip the prompt / allow -auto-approve on destructive plans
  --                  everything after it is passed straight to terraform

Environment:
  TFW_TERRAFORM       terraform binary to use (default "terraform")
  NO_COLOR            disable color
`, version, exitDestructive)
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var code int
	switch os.Args[1] {
	case "plan":
		code = cmdPlan(os.Args[2:])
	case "apply":
		code = cmdApply(os.Args[2:])
	case "check":
		code = cmdCheck(os.Args[2:])
	case "summary", "summarize":
		code = cmdSummary(os.Args[2:])
	case "version", "-v", "--version":
		fmt.Println("tfw " + version)
		return
	case "help", "-h", "--help":
		usage()
		return
	default:
		fmt.Fprintf(os.Stderr, "tfw: unknown command %q\n\n", os.Args[1])
		usage()
		code = 2
	}
	os.Exit(code)
}
