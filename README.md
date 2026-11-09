# tfw — terraform plan/apply guard

A small safeguard around `terraform plan` / `terraform apply`.

* shows the plan summary (same table `tf-summarize` prints — it reuses that
  project's own `parser` / `writer` / `terraformstate` packages)
* if the plan deletes or replaces anything, prints a **big red DESTROY banner**
  listing exactly what will disappear
* then **refuses to apply unless you type the word `destroy`** — not `yes`

```
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

Type "destroy" to apply this plan:
```

## Install

```sh
git clone <this repo> tfw && cd tfw
go build -o tfw .                    # or: go install . (puts tfw in $GOBIN)
install -m 755 tfw /usr/local/bin/   # macOS: /opt/homebrew/bin/ also works
```

Requires Go 1.26+. The binary shells out to `terraform`, so terraform must be
on `PATH` (override with `TFW_TERRAFORM=/path/to/terraform`).

## Usage

```sh
tfw plan                      # terraform plan -out=.tfw/tfw.tfplan, then summary
tfw plan -var-file=prod.tfvars -target=aws_instance.web
tfw apply                     # shows the saved plan, prompts, applies it
tfw apply --force             # skip the prompt (still prints the banner)
tfw check                     # exit 10 if anything is destroyed/replaced (CI gate)
tfw check plan.json           # same, on a JSON plan
tfw summary plan.json         # just the tf-summarize table
```

Flags `--color=auto|always|never`, `--plan-dir=<dir>`, `--force` are tfw's;
**everything else is passed straight to terraform** (`--` stops tfw parsing).

### Rules

| situation | what tfw does |
|---|---|
| plan adds/changes only | prompts like terraform: only `yes` applies |
| plan deletes or replaces | red banner, only `destroy` (lowercase) applies |
| `-auto-approve` on a destructive plan | **blocked** (override with `--force`) |
| no answer / EOF / Ctrl-D | cancels, exit 1 |
| plan file older than 30 min | yellow warning before prompting |

Replaces count as destructive on purpose: `-/+` destroys the old resource even
though the address survives. That is exactly the case that bites you.

Exit codes: `0` ok, `1` aborted/error, `10` destructive plan from `tfw check`.

## CI gate

```yaml
- run: terraform plan -out=tfplan -input=false
- run: terraform show -json tfplan > plan.json
- run: tfw check plan.json      # fails the job (exit 10) if anything is destroyed
- run: terraform apply -input=false tfplan
```

Or run `tfw plan` in the job and gate on the saved `.tfw/tfw.tfplan`.

## Why not just fork tf-summarize?

tf-summarize is a *printer*: input plan JSON, output a table, exit 0. The gate
has to live around `terraform apply` (prompt + exec), which is a different job
and would be an odd thing to bolt into a summarizer. So tfw imports
`github.com/dineshba/tf-summarize/{reader,parser,writer,terraformstate}` and
adds only the gate. No fork to keep in sync — if tf-summarize improves its
table, tfw gets it on the next `go get -u`.

## Notes / limits

* `tfw apply` applies the **saved plan file**, not a fresh plan — that is the
  point (no plan/apply drift), but it means: run `tfw plan` first.
* The `destroy` word is case-sensitive on purpose (`DESTROY`/`Destroy` rejected).
* No network, no state access: it only reads the plan.

## Test

`testdata/` has plan fixtures and a fake `terraform` shim, so the gate can be
tested without cloud credentials:

```sh
PATH="$PWD/testdata/shim:$PATH" ./tfw plan
printf 'yes\n'     | PATH="$PWD/testdata/shim:$PATH" ./tfw apply   # cancels
printf 'destroy\n' | PATH="$PWD/testdata/shim:$PATH" ./tfw apply   # applies
./tfw check testdata/mixed.json    # exit 10
./tfw check testdata/safe.json     # exit 0
```
