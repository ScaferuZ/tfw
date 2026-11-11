// Command tfw is a guard around terraform plan/apply.
//
// It reads the saved plan (JSON, or a binary tfplan via `show -json`), prints
// what will change with the destructive rows first, and refuses to run `apply`
// unless the plan is clean or the user types "destroy".
//
// The same guard works with terragrunt (--terragrunt, `tfw plan --tg`): tfw
// drives `terragrunt plan` / `terragrunt apply` instead of terraform, which is
// what wraps a unit's modules in its own state and cache directory.
//
// This file only wires the subcommands together: flag handling, file paths and
// exit codes. The work lives in internal/plan (read + classify),
// internal/render (terminal output), internal/guard (the confirmation policy)
// and internal/terraform (running terraform or terragrunt).
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ScaferuZ/tfw/internal/guard"
	"github.com/ScaferuZ/tfw/internal/plan"
	"github.com/ScaferuZ/tfw/internal/render"
	"github.com/ScaferuZ/tfw/internal/terraform"
)

const version = "0.3.0"

// exitDestructive is returned by `tfw check` when the plan deletes or replaces
// anything, so CI can gate on it: `tfw check plan.json || exit 1`.
const exitDestructive = 10

// staleAfter is how old a saved plan may be before tfw warns that it may no
// longer match the configuration.
const staleAfter = 30 * time.Minute

// opts are tfw's own flags, as opposed to terraform's.
type opts struct {
	colorMode  string
	planDir    string
	force      bool
	terragrunt bool
}

func defaultOpts() *opts { return &opts{colorMode: "auto", planDir: ".tfw"} }

// tool is the binary tfw drives: terragrunt when --terragrunt was given,
// otherwise plain terraform.
func (o *opts) tool() terraform.Tool {
	if o.terragrunt {
		return terraform.Terragrunt
	}
	return terraform.Terraform
}

// extractTfwFlags pulls tfw's flags out of args wherever they appear and returns
// everything else untouched, so terraform/terragrunt flags reach the tool
// verbatim (`tfw plan -var-file=x.tfvars -out=tfplan` just works). A bare "--"
// stops tfw from interpreting anything after it.
func extractTfwFlags(args []string, o *opts) []string {
	rest := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			return append(rest, args[i+1:]...)
		case a == "--force" || a == "-force":
			o.force = true
		case a == "--terragrunt" || a == "-terragrunt" || a == "--tg" || a == "-tg":
			o.terragrunt = true
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

// savedPlan returns the path tfw uses for the plan file, resolved to an
// absolute path when terragrunt is driving: terragrunt runs terraform inside a
// per-module cache directory, so a relative -out or plan argument would be
// resolved there instead of in the directory tfw was invoked from.
func savedPlan(o *opts) string {
	p := filepath.Join(o.planDir, "tfw.tfplan")
	if !o.tool().Wraps() {
		return p
	}
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return p
}

// resolvePlan does the same for a plan file the user named themselves.
func resolvePlan(o *opts, p string) string {
	if !o.tool().Wraps() {
		return p
	}
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return p
}

// hintTerragrunt nudges towards --terragrunt when the current directory looks
// like a terragrunt unit (a terragrunt.hcl and no *.tf files of its own) and
// tfw is about to run plain terraform in it.
func hintTerragrunt(t terraform.Tool) {
	if t.Wraps() {
		return
	}
	if _, err := os.Stat("terragrunt.hcl"); err != nil {
		return
	}
	if m, _ := filepath.Glob("*.tf"); len(m) > 0 {
		return
	}
	fmt.Fprintln(os.Stderr, "tfw: terragrunt.hcl found and no *.tf here - pass --terragrunt to drive terragrunt")
}

// cmdPlan runs the tool's plan into the plan directory, then shows the result.
func cmdPlan(raw []string) int {
	o := defaultOpts()
	tfArgs := extractTfwFlags(raw, o)
	tool := o.tool()
	style := render.NewStyle(o.colorMode)
	hintTerragrunt(tool)

	planPath := savedPlan(o)
	if err := os.MkdirAll(o.planDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "tfw: %v\n", err)
		return 1
	}

	args := append([]string{"plan"}, tfArgs...)
	if terraform.HasOutFlag(tfArgs) {
		planPath = resolvePlan(o, terraform.OutFlagValue(tfArgs))
	} else {
		args = append(args, "-out="+planPath)
	}

	// 2 is terraform's -detailed-exitcode "changes present".
	if code := tool.Run(args...); code != 0 && code != 2 {
		return code
	}

	p, err := plan.LoadWith(planPath, tool)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tfw: %v\n", err)
		return 1
	}
	sum := p.Summarize()

	fmt.Println()
	if err := style.Table(os.Stdout, sum); err != nil {
		fmt.Fprintf(os.Stderr, "tfw: %v\n", err)
		return 1
	}
	fmt.Println()
	style.Counts(os.Stdout, sum)
	if sum.Destructive() {
		style.Banner(os.Stdout, sum, planPath)
	}
	next := "tfw apply"
	if tool.Wraps() {
		next += " --terragrunt"
	}
	fmt.Printf("\nNext: %s\n", style.Paint(render.Bold, next))
	return 0
}

// cmdApply shows the saved plan, asks for the confirmation the guard demands,
// then applies that exact plan file.
func cmdApply(raw []string) int {
	o := defaultOpts()
	tfArgs := extractTfwFlags(raw, o)
	tool := o.tool()
	style := render.NewStyle(o.colorMode)
	hintTerragrunt(tool)

	planPath := savedPlan(o)
	if v, ok := positionalPlan(tfArgs); ok {
		planPath = resolvePlan(o, v)
	}

	p, err := plan.LoadWith(planPath, tool)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tfw: %v\n", err)
		return 1
	}
	if fi, err := os.Stat(planPath); err == nil {
		if age := time.Since(fi.ModTime()); age > staleAfter {
			fmt.Printf("%s plan file is %s old - re-run `tfw plan` if the config changed\n",
				style.Paint(render.Yellow, "WARN"), age.Round(time.Minute))
		}
	}

	sum := p.Summarize()
	fmt.Println()
	if err := style.Table(os.Stdout, sum); err != nil {
		fmt.Fprintf(os.Stderr, "tfw: %v\n", err)
		return 1
	}
	fmt.Println()
	style.Counts(os.Stdout, sum)

	if sum.Destructive() {
		style.Banner(os.Stdout, sum, planPath)
	}

	decision := guard.Decide(guard.Request{
		Summary:     sum,
		AutoApprove: tool.UsesAutoApprove(tfArgs),
		Force:       o.force,
	})
	if decision.Blocked {
		fmt.Fprintf(os.Stderr, "\n%s tfw: %s\n", style.Paint(render.Red, "BLOCKED"), decision.Reason)
		return 1
	}
	if decision.Prompt {
		question := "Apply this plan? Only 'yes' will be accepted:"
		if decision.Word == guard.WordDestroy {
			question = style.Paint(render.Red, `Type "destroy" to apply this plan:`)
		}
		fmt.Println()
		if !guard.Confirm(os.Stdout, os.Stdin, question, decision.Word) {
			if decision.Word == guard.WordDestroy {
				fmt.Println(style.Paint(render.Red, "Apply cancelled. Nothing was changed."))
			} else {
				fmt.Println(style.Paint(render.Red, "Apply cancelled."))
			}
			return 1
		}
	}
	return tool.Run(append([]string{"apply", planPath}, tfArgs...)...)
}

// cmdCheck prints the counts and the destroyed addresses, exiting 10 when the
// plan is destructive. This is the CI entry point.
func cmdCheck(raw []string) int {
	o := defaultOpts()
	args := extractTfwFlags(raw, o)
	style := render.NewStyle(o.colorMode)

	planPath := savedPlan(o)
	if len(args) > 0 {
		planPath = resolvePlan(o, args[0])
	}

	p, err := plan.LoadWith(planPath, o.tool())
	if err != nil {
		fmt.Fprintf(os.Stderr, "tfw: %v\n", err)
		return 1
	}
	sum := p.Summarize()
	style.Counts(os.Stdout, sum)
	if !sum.Destructive() {
		return 0
	}
	for _, addr := range sum.Destroy {
		fmt.Println(style.Paint(render.Red, "  - "+addr))
	}
	for _, addr := range sum.Replace {
		fmt.Println(style.Paint(render.Red, "  -/+ "+addr))
	}
	return exitDestructive
}

// cmdSummary prints just the plan table.
func cmdSummary(raw []string) int {
	o := defaultOpts()
	args := extractTfwFlags(raw, o)
	style := render.NewStyle(o.colorMode)

	planPath := savedPlan(o)
	if len(args) > 0 {
		planPath = resolvePlan(o, args[0])
	}

	p, err := plan.LoadWith(planPath, o.tool())
	if err != nil {
		fmt.Fprintf(os.Stderr, "tfw: %v\n", err)
		return 1
	}
	if err := style.Table(os.Stdout, p.Summarize()); err != nil {
		fmt.Fprintf(os.Stderr, "tfw: %v\n", err)
		return 1
	}
	return 0
}

// hoistTfwFlags lets tfw's own flags appear before the subcommand
// (`tfw --terragrunt plan` == `tfw plan --terragrunt`) by moving them in front
// of the subcommand's arguments. It returns the subcommand and its arguments;
// when the first argument is not a tfw flag, nothing is moved.
func hoistTfwFlags(args []string) (string, []string) {
	pre := make([]string, 0, len(args))
	i := 0
	for i < len(args) {
		a := args[i]
		switch {
		case a == "--force" || a == "-force" || a == "--terragrunt" || a == "-terragrunt" ||
			a == "--tg" || a == "-tg":
			pre = append(pre, a)
			i++
		case a == "--color" || a == "-color" || a == "--plan-dir" || a == "-plan-dir":
			pre = append(pre, a)
			if i+1 < len(args) {
				pre = append(pre, args[i+1])
				i++
			}
			i++
		case strings.HasPrefix(a, "--color=") || strings.HasPrefix(a, "-color=") ||
			strings.HasPrefix(a, "--plan-dir=") || strings.HasPrefix(a, "-plan-dir="):
			pre = append(pre, a)
			i++
		default:
			cmd := a
			return cmd, append(pre, args[i+1:]...)
		}
	}
	return "", nil
}

// positionalPlan finds a plan file passed as a bare argument.
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
  tfw plan  [terraform plan flags...]   run plan, save the plan file, show summary + DESTROY banner
  tfw apply [terraform apply flags...]  show saved plan, then require "destroy" (or "yes") before applying
  tfw check [plan.json|tfplan]          print counts; exit %d if anything is destroyed/replaced (CI gate)
  tfw summary [plan.json|tfplan]        plan table view (destroys first)
  tfw version

Common flags:
  --terragrunt, --tg  drive terragrunt instead of terraform (same subcommands, same gate)
  --plan-dir string   where the saved plan lives (default ".tfw")
  --color string      auto|always|never (default "auto")
  --force             skip the prompt / allow -auto-approve on destructive plans
  --                  everything after it is passed straight to the tool

Environment:
  TFW_TERRAFORM       terraform binary to use (default "terraform")
  TFW_TERRAGRUNT      terragrunt binary to use (default "terragrunt")
  NO_COLOR            disable color

tfw's own flags may also be written before the subcommand:
  tfw --terragrunt plan   ==   tfw plan --terragrunt

Terragrunt notes:
  run it in a directory with a terragrunt.hcl; the saved plan is written with an
  absolute path because terragrunt runs terraform in its own cache directory.
  "terragrunt run-all" is not supported: it plans many units into many files.
`, version, exitDestructive)
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	cmd, rest := hoistTfwFlags(os.Args[1:])
	switch cmd {
	case "version", "-v", "--version":
		fmt.Println("tfw " + version)
	case "help", "-h", "--help", "":
		usage()
	case "plan":
		os.Exit(cmdPlan(rest))
	case "apply":
		os.Exit(cmdApply(rest))
	case "check":
		os.Exit(cmdCheck(rest))
	case "summary", "summarize":
		os.Exit(cmdSummary(rest))
	default:
		fmt.Fprintf(os.Stderr, "tfw: unknown command %q\n\n", cmd)
		usage()
		os.Exit(2)
	}
}
