# tfw — terraform destroy guard

**A safety guard for `terraform plan` / `terraform apply`: makes destroys impossible to miss and impossible to confirm by muscle memory.**

[![ci](https://github.com/ScaferuZ/tfw/actions/workflows/ci.yml/badge.svg)](https://github.com/ScaferuZ/tfw/actions/workflows/ci.yml)
[![Go](https://img.shields.io/badge/Go-1.26+-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

Terraform asks you to type `yes` for every apply. After a 12-hour day `yes` is
reflex — and a `-/+` replace that quietly recreates your database looks like any
other change. `tfw` sits in front of apply
and refuses to continue unless the plan is clean, or unless you type
**`destroy`** when it is not.

- 🔴 Big red **DESTROY banner** listing the exact addresses that will be deleted or replaced
- ⌨️ Destructive plan → must type `destroy` (case-sensitive). `yes`, `DESTROY`, empty input and EOF all cancel
- 🚫 `terraform apply -auto-approve` is **blocked** on a destructive plan (`--force` is the deliberate override)
- 📋 Plan table with the destructive rows **first** (own renderer — no third-party dependencies at all)
- 🧮 `tfw check` exits **10** when anything is destroyed or replaced — use it as a CI gate
- ⏱ Warns when the saved plan file is older than 30 minutes (no silently applying a stale plan)
- 🔒 Applies the **saved plan file**, so what you reviewed is exactly what runs
- 🌍 No network, no state access: it only reads the plan file
- 📦 Zero third-party dependencies, 4.3 MB static binary

## Demo

```
$ tfw plan
Terraform will perform the following actions:
...
Plan: 2 to add, 1 to change, 1 to destroy.
Saved the plan to: .tfw/tfw.tfplan

CHANGE        RESOURCE
destroy (1)   aws_db_instance.legacy
replace (1)   aws_instance.bastion
update (1)    aws_security_group.web
add (2)       aws_instance.web
              aws_s3_bucket.assets

Plan: +2 add  ~1 change  -1 destroy  -/+1 replace

████████████████████████████████████████████████████████████████████████████
██                                                                        ██
██   !!  D E S T R O Y  !!                                                ██
██                                                                        ██
██   1 resource(s) will be DELETED                                        ██
██   1 resource(s) will be REPLACED (destroy + create)                    ██
██                                                                        ██
██   x aws_db_instance.legacy                                             ██
██   x aws_instance.bastion                                               ██
██                                                                        ██
██   apply requires typing the word "destroy"                             ██
████████████████████████████████████████████████████████████████████████████

$ tfw apply
Type "destroy" to apply this plan: yes
Apply cancelled. Nothing was changed.        # exit 1, terraform never ran

$ tfw apply
Type "destroy" to apply this plan: destroy
terraform apply .tfw/tfw.tfplan              # gate passed
```

## Why replaces are gated too

`terraform plan` shows a replace as `-/+`. The address survives, the resource
does not: the old object is destroyed and a new one created. Counting only
`"delete"` actions — the obvious `jq` one-liner — misses it. `tfw` counts both:

| plan action | meaning | gated? |
|---|---|---|
| `["delete"]` | destroyed | yes |
| `["delete","create"]` | replaced (destroyed first) | yes |
| `["create","delete"]` | create-before-destroy — new object first, old one still deleted | yes |
| `["create"]` / `["update"]` / `["no-op"]` | add / change in place / nothing | no |

## Install

```sh
# macOS / Linux, prebuilt binary (no Go needed)
brew install ScaferuZ/tap/tfw

# or with Go 1.26+
go install github.com/ScaferuZ/tfw@latest

# or download a release binary
curl -sSL https://github.com/ScaferuZ/tfw/releases/latest/download/tfw_linux_amd64.tar.gz \
  | tar xz -C /usr/local/bin tfw
```

From source:

```sh
git clone https://github.com/ScaferuZ/tfw
cd tfw && go build -o tfw .
install -m 755 tfw /usr/local/bin/     # macOS: /opt/homebrew/bin/
```

`terraform` must be on `PATH` (override with `TFW_TERRAFORM=/path/to/terraform`).

## Layout

```
main.go                  subcommands, flag handling, exit codes — no logic
internal/plan            read a saved plan, classify into destroy/replace/update/add/import/moved
internal/guard           the confirmation policy: which word, when to block
internal/render          terminal output: counts line, table, DESTROY banner, colour
internal/terraform       locate/run terraform, convert a binary tfplan to JSON
testdata/                plan fixtures + the fake terraform the tests drive
```

Everything except `main` lives under `internal/`, which the compiler enforces:
no other module can import it, so the API can change without a deprecation
cycle. Each package is named for what it provides, not for the kind of code it
contains ([Organizing a Go module](https://go.dev/doc/modules/layout)).

The split is deliberately shallow — four small packages, no `pkg/`, no
`models/`/`utils/`: those layers only pay off once several binaries or an
external importer exist. Tests live next to the code they cover, except the gate
end-to-end test, which builds the real binary (`main_test.go`).

## Usage

```sh
tfw plan                          # terraform plan -out=.tfw/tfw.tfplan, then summary + banner
tfw plan -var-file=prod.tfvars -target=aws_instance.web
tfw apply                         # summarize the saved plan, prompt, apply it
tfw apply --force                 # skip the prompt (banner is still printed)
tfw check                         # exit 10 if anything is destroyed/replaced
tfw check plan.json               # same, on a JSON plan
tfw summary plan.json             # just the summary table
```

Flags `--color=auto|always|never`, `--plan-dir=<dir>`, `--force` belong to
`tfw`; **everything else is passed to terraform unchanged**. `--` stops tfw
parsing, so `tfw plan -- -out=custom.tfplan` works.

| situation | behaviour |
|---|---|
| plan adds/changes only | prompts like terraform: only `yes` applies |
| plan deletes or replaces | red banner, only `destroy` (lowercase) applies |
| `-auto-approve` + destructive | **blocked**, `--force` to override |
| no answer / EOF / Ctrl-D | cancels, exit 1 |
| saved plan > 30 min old | yellow warning before prompting |

Exit codes: `0` ok · `1` aborted/error · `10` destructive plan (from `check`).

## Use it as a CI gate

Fail the pipeline when a plan destroys infrastructure, before anything runs:

```yaml
# .github/workflows/terraform.yml
- run: terraform plan -input=false -out=tfplan
- run: terraform show -json tfplan > plan.json
- run: tfw check plan.json        # exit 10 -> job fails, apply never runs
- run: terraform apply -input=false tfplan
```

GitLab CI / Jenkins / shell:

```sh
terraform show -json tfplan > plan.json
tfw check plan.json || { echo "plan destroys infrastructure"; exit 1; }
```

## `tfw` vs. everything else

| tool | what it does | gates destroys? |
|---|---|---|
| `terraform apply` (native) | colour, `Plan: N to destroy`, `yes` prompt | no stronger confirmation, no replace gate, `-auto-approve` walks through |
| [`tf-summarize`](https://github.com/dineshba/tf-summarize) | pretty plan summary table | no — it is a printer |
| [`rover`](https://github.com/im2nguyen/rover) | interactive plan/state visualisation in the browser | no |
| [Atlantis](https://www.runatlantis.io/) / [Digger](https://digger.dev/) | PR bot: plan on PR, apply on approve | only with a policy hook |
| [terraform-compliance](https://github.com/terraform-compliance/cli) / OPA `conftest` / Sentinel | policy as code (Gherkin / Rego) | yes, but you write the policies |
| Spacelift / env0 | commercial IaC platforms with policies | yes, paid |
| **tfw** | local wrapper: summarise + force `destroy` + CI exit code | yes, zero config |

They are complementary: `tfw` is the cheap local seatbelt, policy engines are
the org-wide rules. Run `tfw check` in CI even if you already run policies.

## FAQ

**Does terraform warn before destroying resources?**
It prints destroyed resources in red and a `Plan: N to destroy` line, and
`terraform apply` asks you to type `yes`. There is no separate confirmation for
deletes, `-auto-approve` skips the prompt entirely, and CI logs rarely show the
colour.

**How do I see what terraform will destroy before applying?**

```sh
terraform plan -out=tfplan
terraform show -json tfplan \
  | jq -r '.resource_changes[] | select(.change.actions|index("delete")) | .address'
```

…or `tfw plan`, which prints the table and a banner listing every address.

**How do I prevent `terraform apply` from destroying infrastructure in CI?**
Run `tfw check plan.json` (exit `10`) as a step before apply — see above. Plain
`jq` works too, but remember `["delete","create"]` replaces.

**Can I protect one specific resource, e.g. always refuse to destroy RDS?**
Not yet — that is policy-engine territory (`conftest`/OPA). A per-resource
allow/deny list is on the roadmap.

**Does it depend on tf-summarize or terraform-json?**
No. `go.mod` has zero `require` lines: the plan model and the table renderer are
~180 lines of stdlib in `plan.go`. An earlier prototype imported tf-summarize's
parser/writer for the table; that is gone, so there is no upstream to keep in
sync and no dependency that can break a build. Binary: 4.3 MB static.

## Development

```sh
go test ./...        # unit tests + end-to-end gate tests
```

The end-to-end tests use `testdata/shim/terraform`, a fake terraform that writes
a fixture plan, so the gate is proven without cloud credentials:

```sh
PATH="$PWD/testdata/shim:$PATH" go run . plan
printf 'yes\n'     | PATH="$PWD/testdata/shim:$PATH" go run . apply   # cancels
printf 'destroy\n' | PATH="$PWD/testdata/shim:$PATH" go run . apply   # applies
```

## Roadmap

- [ ] protected-resource allowlist (`--protect module.rds.*`)
- [ ] `tfw diff` between two saved plans
- [ ] git hook installer (`tfw install-hook`)

## Contributing

Issues and PRs welcome — especially new gate rules (protected resources, plan
diffing). Run `go test ./...` before sending anything; the gate tests use the
fake terraform in `testdata/shim/` so no cloud account is needed.

## License

MIT — see [LICENSE](LICENSE).

---

**Keywords:** terraform plan guard · terraform destroy guard · prevent terraform
destroy · terraform apply confirmation prompt · terraform plan summary ·
terraform CI gate · terraform safety wrapper · terraform replace `-/+` ·
block terraform auto-approve · infrastructure as code safety · terraform
destructive change detection
