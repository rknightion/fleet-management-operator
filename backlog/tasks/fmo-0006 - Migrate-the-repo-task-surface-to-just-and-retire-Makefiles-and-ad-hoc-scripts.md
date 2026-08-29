---
id: FMO-0006
title: Migrate the repo task surface to just and retire Makefiles and ad-hoc scripts
status: To Do
assignee: []
created_date: '2026-08-28 19:22'
updated_date: '2026-08-29 10:42'
labels:
  - 'wave:2-fleet'
dependencies: []
priority: medium
type: chore
ordinal: 6000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
# Migrate fleet-management-operator to `just`

Fleet-wide `just` migration. This repo's authoritative spec is the frozen standard doc — do not
re-derive vocabulary, groups, header shape, or CI rules; they are settled. This task tells you
exactly what to do in *this* repo.

## 1. Outcome

`fleet-management-operator` (Go kubebuilder operator, module `github.com/grafana/fleet-management-operator`)
has one top-level `justfile` implementing the fleet's mandatory seven-recipe vocabulary plus the
repo's real optional recipes (`typecheck`, `build`, `run`, `gen`, `gen-check`, `docs`, `ci`). The
373-line `Makefile` is deleted. `.github/workflows/ci.yaml`, `test.yml`, and `e2e.yaml` call `just
<recipe>` instead of `make <target>`. `CONTRIBUTING.md`, `AGENTS.md`/`CLAUDE.md`, and `README.md`
tell contributors to run `just`, not `make`. `backlog/config.yml`'s `definition_of_done` names
`just` recipes. `just check` passes locally and is exactly what CI's `ci-success` job gates on.
`.devcontainer/post-install.sh` is untouched — it bootstraps a devcontainer that has no `just` yet
and stays a file.

## 2. The complete justfile

Drop this at the repo root as `justfile`. All commands are this repo's real toolchain (kubebuilder
Makefile scaffold, `golangci-lint` v2, `helm-docs`, `crd-ref-docs`, `controller-gen`, `kustomize`,
`setup-envtest`). Tool versions match the current `Makefile` exactly — keep them in sync if the
Makefile has moved since this task was filed.

```just
set shell := ["bash", "-euo", "pipefail", "-c"]

# show the task surface
default:
    @just --list

# --- tool versions (match Makefile at time of migration; bump here going forward) ---
kustomize_version := "v5.7.1"
controller_tools_version := "v0.20.0"
golangci_lint_version := "v2.13.1"
helm_docs_version := "v1.14.2"
crd_ref_docs_version := "v0.3.0"

# controller-runtime's resolved version drives the envtest release branch; k8s.io/api's
# resolved version drives the envtest Kubernetes version. Computed at parse time via `go list`,
# exactly like the Makefile's $(call gomodver,...) — this is a top-level `:=` backtick
# assignment, so the go-template `{{ }}` inside the format string is NOT touched by just's own
# `{{ }}` interpolation (verified: that only applies inside recipe bodies). Do not move this
# computation into a recipe body without escaping — see trap #1 in the task.
_ctrl_runtime_ver := `go list -m -f '{{if .Replace}}{{.Replace.Version}}{{else}}{{.Version}}{{end}}' sigs.k8s.io/controller-runtime 2>/dev/null || true`
_k8s_api_ver := `go list -m -f '{{if .Replace}}{{.Replace.Version}}{{else}}{{.Version}}{{end}}' k8s.io/api 2>/dev/null || true`
envtest_version := `v='` + _ctrl_runtime_ver + `'; [ -n "$v" ] || { echo "Set envtest_version manually (controller-runtime replace has no tag)" >&2; exit 1; }; printf '%s\n' "$v" | sed -E 's/^v?([0-9]+)\.([0-9]+).*/release-\1.\2/'`
envtest_k8s_version := `v='` + _k8s_api_ver + `'; [ -n "$v" ] || { echo "Set envtest_k8s_version manually (k8s.io/api replace has no tag)" >&2; exit 1; }; printf '%s\n' "$v" | sed -E 's/^v?[0-9]+\.([0-9]+).*/1.\1/'`

localbin := justfile_directory() / "bin"
img := env("IMG", "ghcr.io/rknightion/fleet-management-operator:dev")
platforms := "linux/arm64,linux/amd64"
builder_name := "fm-crd-builder"
kind_cluster := env("KIND_CLUSTER", "fm-crd-test-e2e")
helm_release := env("HELM_RELEASE", "fleet-management-operator")
helm_namespace := env("HELM_NAMESPACE", "fleet-management-system")

# --- private tool installer, mirrors Makefile's go-install-tool with a versioned symlink ---
[private]
[script('bash')]
_install-tool bin pkg version:
    mkdir -p "{{ localbin }}"
    target="{{ localbin }}/{{ bin }}"
    versioned="{{ localbin }}/{{ bin }}-{{ version }}"
    if [ -x "$versioned" ] && [ "$(readlink -- "$target" 2>/dev/null)" = "{{ bin }}-{{ version }}" ]; then
        exit 0
    fi
    echo "Downloading {{ pkg }}@{{ version }}"
    rm -f "$target"
    GOBIN="{{ localbin }}" go install "{{ pkg }}@{{ version }}"
    mv "{{ localbin }}/{{ bin }}" "$versioned"
    ln -sf "{{ bin }}-{{ version }}" "$target"

[private]
[script('bash')]
_install-envtest:
    mkdir -p "{{ localbin }}"
    GOBIN="{{ localbin }}" go install "sigs.k8s.io/controller-runtime/tools/setup-envtest@{{ envtest_version }}"

# install dev tools (controller-gen, kustomize, envtest, golangci-lint, helm-docs, crd-ref-docs) into ./bin
setup:
    just _install-tool kustomize sigs.k8s.io/kustomize/kustomize/v5 {{ kustomize_version }}
    just _install-tool controller-gen sigs.k8s.io/controller-tools/cmd/controller-gen {{ controller_tools_version }}
    just _install-tool golangci-lint github.com/golangci/golangci-lint/v2/cmd/golangci-lint {{ golangci_lint_version }}
    just _install-tool helm-docs github.com/norwoodj/helm-docs/cmd/helm-docs {{ helm_docs_version }}
    just _install-tool crd-ref-docs github.com/elastic/crd-ref-docs {{ crd_ref_docs_version }}
    just _install-envtest
    @echo "setup complete: {{ localbin }}"

# format Go source in place
[group('check')]
fmt:
    go fmt ./...

# verify formatting (Go + justfile) without changing anything
[group('check')]
[no-exit-message]
fmt-check:
    test -z "$(gofmt -l .)" || { gofmt -l .; echo "run 'just fmt'"; exit 1; }
    just --fmt --check

# go vet
[group('check')]
[no-exit-message]
typecheck:
    go vet ./...

# verify go.mod/go.sum are tidy
[group('check')]
[no-exit-message]
mod-tidy-check:
    go mod tidy
    git diff --exit-code -- go.mod go.sum

# static analysis (golangci-lint)
[group('check')]
[no-exit-message]
lint: mod-tidy-check
    "{{ localbin }}/golangci-lint" run ./...

# apply golangci-lint auto-fixes
[group('check')]
lint-fix:
    "{{ localbin }}/golangci-lint" run --fix ./...

# verify golangci-lint's own config is valid
[group('check')]
[no-exit-message]
lint-config:
    "{{ localbin }}/golangci-lint" config verify

# run the test suite (filter="" runs everything; pass a go test -run pattern to narrow)
[group('check')]
[no-exit-message]
test filter="": gen typecheck _install-envtest
    assets="$("{{ localbin }}/setup-envtest" use {{ envtest_k8s_version }} --bin-dir "{{ localbin }}" -p path)"; \
    KUBEBUILDER_ASSETS="$assets" go test -race $(go list ./... | grep -v /e2e) -run '{{ filter }}' -coverprofile cover.out

# stand up (or reuse) a Kind cluster for e2e tests
[group('infra')]
setup-test-e2e:
    command -v kind >/dev/null 2>&1 || { echo "kind is not installed"; exit 1; }
    kind get clusters 2>/dev/null | grep -qx "{{ kind_cluster }}" || kind create cluster --name "{{ kind_cluster }}"

# tear down the e2e Kind cluster
[confirm('delete the e2e Kind cluster? [y/N]')]
[group('infra')]
cleanup-test-e2e:
    kind delete cluster --name "{{ kind_cluster }}"

# run e2e tests against an isolated Kind cluster (KIND_CLUSTER env overrides the cluster name)
[group('check')]
test-e2e: setup-test-e2e gen typecheck
    KIND_CLUSTER="{{ kind_cluster }}" go test -tags=e2e ./test/e2e/ -v -ginkgo.v

# regenerate CRD manifests + RBAC + webhook config
[group('gen')]
manifests:
    "{{ localbin }}/controller-gen" rbac:roleName=manager-role crd webhook paths="./..." output:crd:artifacts:config=config/crd/bases

# regenerate DeepCopy/DeepCopyInto/DeepCopyObject code
[group('gen')]
generate:
    "{{ localbin }}/controller-gen" object:headerFile="hack/boilerplate.go.txt" paths="./..."

# regenerate all committed generated code (manifests + deepcopy)
[group('gen')]
gen: manifests generate

# fail if regenerating manifests/deepcopy leaves the tree dirty
[group('gen')]
[no-exit-message]
gen-check: gen
    git diff --exit-code -- config/ api/ || { echo "generated code is stale; run 'just gen' and commit"; exit 1; }

# regenerate charts/fleet-management-operator/README.md from values.yaml
[group('gen')]
chart-docs:
    "{{ localbin }}/helm-docs" --chart-search-root charts

# verify the chart README matches values.yaml
[group('check')]
[no-exit-message]
chart-docs-check:
    "{{ localbin }}/helm-docs" --chart-search-root charts --dry-run > /tmp/helm-docs.expected
    diff -q /tmp/helm-docs.expected charts/fleet-management-operator/README.md > /dev/null 2>&1 || { echo "charts/fleet-management-operator/README.md is out of date. Run 'just chart-docs'."; exit 1; }

# regenerate docs/api-reference.md from api/v1alpha1 godoc + CRD bases
[group('gen')]
[script('bash')]
api-docs:
    tmp=$(mktemp)
    "{{ localbin }}/crd-ref-docs" \
        --source-path=api/v1alpha1 \
        --config=.crd-ref-docs.yaml \
        --renderer=markdown \
        --output-path="$tmp"
    cat hack/api-reference-front-matter.md "$tmp" > docs/api-reference.md
    rm -f "$tmp"

# verify docs/api-reference.md matches API types
[group('check')]
[no-exit-message]
[script('bash')]
api-docs-check:
    tmp=$(mktemp); gen=$(mktemp)
    "{{ localbin }}/crd-ref-docs" \
        --source-path=api/v1alpha1 \
        --config=.crd-ref-docs.yaml \
        --renderer=markdown \
        --output-path="$gen" >/dev/null
    cat hack/api-reference-front-matter.md "$gen" > "$tmp"
    rm -f "$gen"
    diff -q "$tmp" docs/api-reference.md > /dev/null 2>&1 || { echo "docs/api-reference.md is out of date. Run 'just api-docs'."; rm -f "$tmp"; exit 1; }
    rm -f "$tmp"

# regenerate all auto-generated docs (api reference, chart README, flags/metrics/events/samples)
[group('gen')]
docs: api-docs chart-docs
    go run ./hack/docgen flags   --out docs/flags.md
    go run ./hack/docgen metrics --out docs/metrics.md
    go run ./hack/docgen events  --out docs/events.md
    go run ./hack/docgen samples --out docs/samples.md

# verify all auto-generated docs are up to date
[group('check')]
[no-exit-message]
docs-check: api-docs-check chart-docs-check
    go run ./hack/docgen flags   --out docs/flags.md   --check
    go run ./hack/docgen metrics --out docs/metrics.md --check
    go run ./hack/docgen events  --out docs/events.md  --check
    go run ./hack/docgen samples --out docs/samples.md --check
    go run ./hack/docgen verify-conditions docs/conditions.md

# lint + template-render the Helm chart (same overrides CI uses)
[group('check')]
[no-exit-message]
[script('bash')]
helm-lint:
    set_flags=(--set fleetManagement.baseUrl=https://test --set fleetManagement.username=test --set fleetManagement.password=test)
    helm lint charts/fleet-management-operator "${set_flags[@]}"
    helm template test charts/fleet-management-operator "${set_flags[@]}" > /dev/null
    helm template test charts/fleet-management-operator "${set_flags[@]}" > /tmp/chart-default.yaml
    if grep -q "kind: NetworkPolicy" /tmp/chart-default.yaml; then
        echo "NetworkPolicy should be disabled by default"
        exit 1
    fi
    helm template test charts/fleet-management-operator "${set_flags[@]}" --set networkPolicy.enabled=true > /tmp/chart-networkpolicy.yaml
    grep -q "kind: NetworkPolicy" /tmp/chart-networkpolicy.yaml

# THE GATE — everything ci-success in ci.yaml enforces
[group('check')]
check: fmt-check lint typecheck test gen-check installer-check docs-check helm-lint

# check plus e2e — the full local surface, matching ci.yaml + e2e.yaml combined
[group('check')]
ci: check test-e2e

# build the manager binary
[group('build')]
build: gen fmt typecheck
    go build -o bin/manager cmd/main.go

# run the controller locally against the current kubeconfig (long-running)
[group('dev')]
run: gen fmt typecheck
    go run ./cmd/main.go

[private]
_docker-buildx-setup:
    docker buildx inspect {{ builder_name }} >/dev/null 2>&1 && docker buildx use {{ builder_name }} \
        || docker buildx create --name {{ builder_name }} --driver docker-container --bootstrap --use

# remove the multi-arch buildx builder
[group('build')]
docker-buildx-remove:
    docker buildx inspect {{ builder_name }} >/dev/null 2>&1 && docker buildx rm {{ builder_name }} || true

# build and push a multi-arch manager image (IMG env overrides the tag)
[confirm('push a multi-arch image to a registry? [y/N]')]
[group('build')]
docker-build: _docker-buildx-setup
    docker buildx build --builder {{ builder_name }} --platform={{ platforms }} --tag {{ img }} --push .

# build a manager image for the local architecture and load it into docker (no push)
[group('build')]
docker-build-load:
    docker buildx build --tag {{ img }} --load .

# build the mock Fleet Management API image used by e2e tests
[group('build')]
docker-build-mock-api:
    docker build -t mock-fleet-api:test test/mockapi/

# regenerate dist/install.yaml (consolidated CRDs + deployment manifest)
[group('build')]
build-installer: gen
    mkdir -p dist
    cd config/manager && "{{ localbin }}/kustomize" edit set image controller={{ img }}
    "{{ localbin }}/kustomize" build config/default > dist/install.yaml

# verify dist/install.yaml is committed and up to date
[group('check')]
[no-exit-message]
installer-check: build-installer
    git diff --exit-code -- dist/install.yaml || { echo "dist/install.yaml is out of date. Run 'just build-installer' and commit."; exit 1; }

# install CRDs into the cluster in the current kubeconfig
[group('dev')]
install: manifests
    out="$("{{ localbin }}/kustomize" build config/crd 2>/dev/null || true)"; \
    if [ -n "$out" ]; then echo "$out" | kubectl apply -f -; else echo "No CRDs to install; skipping."; fi

# remove CRDs from the cluster in the current kubeconfig
[confirm('delete CRDs from the current kubeconfig cluster? [y/N]')]
[group('dev')]
uninstall: manifests
    out="$("{{ localbin }}/kustomize" build config/crd 2>/dev/null || true)"; \
    if [ -n "$out" ]; then echo "$out" | kubectl delete --ignore-not-found=true -f -; else echo "No CRDs to delete; skipping."; fi

# deploy the controller via the Helm chart (HELM_VALUES/HELM_RELEASE/HELM_NAMESPACE env to override)
[confirm('deploy/upgrade the controller on the current kubeconfig cluster? [y/N]')]
[group('dev')]
deploy: manifests
    helm upgrade --install {{ helm_release }} charts/fleet-management-operator \
        --namespace {{ helm_namespace }} \
        --create-namespace \
        --set image.repository=$(echo "{{ img }}" | sed 's/:[^:]*$//') \
        --set image.tag=$(echo "{{ img }}" | sed 's/.*://') \
        ${HELM_VALUES:+--values "$HELM_VALUES"} \
        ${HELM_ARGS:-}

# remove the controller Helm release
[confirm('uninstall the controller Helm release? [y/N]')]
[group('dev')]
undeploy:
    helm uninstall {{ helm_release }} --namespace {{ helm_namespace }} 2>/dev/null || true

```

## 3. Makefile disposition

Every target in `Makefile` (373 lines, kubebuilder scaffold + repo additions) → its replacement.
After the justfile above is in place, verified locally, and CI is green on it: `git rm Makefile`.

| Make target | just recipe | Notes |
|---|---|---|
| `help` | `default` (`@just --list`) | Mandatory vocab; `just --list` is strictly better (groups, doc comments). |
| `manifests` | `manifests` | Same `controller-gen` invocation, tool resolved from `bin/` via `setup`. |
| `generate` | `generate` | Same. |
| `fmt` | `fmt` | Same (`go fmt ./...`). |
| `vet` | `typecheck` | Renamed to fleet vocabulary; body unchanged. |
| `test` | `test filter=""` | **Dropped `fmt` from the prerequisite chain** — `test` may not mutate under the fleet contract; see trap #2. Added optional `filter` param per §1 of the standard. |
| `setup-test-e2e` | `setup-test-e2e` | Same logic, `kind_cluster` var. |
| `test-e2e` | `test-e2e` | Dropped `fmt` prerequisite for the same reason as `test`; kept `manifests`/`generate`/`vet` via `gen`+`typecheck`. |
| `cleanup-test-e2e` | `cleanup-test-e2e` | Added `[confirm]` (deletes a cluster). |
| `lint` | `lint` | Now depends on `mod-tidy-check` (folds in the `go mod tidy` diff check that lived in `ci.yaml`'s Lint job, not in the Makefile — see CI changes). |
| `lint-fix` | `lint-fix` | Same. |
| `lint-config` | `lint-config` | Same; not part of `check` (CI never calls it either). |
| `chart-docs` | `chart-docs` | Same. |
| `chart-docs-check` | `chart-docs-check` | Same; now also feeds `check` directly (was already called separately by CI's verify-helm job). |
| `api-docs` | `api-docs` | Same body, `[script('bash')]` since it's multi-line with `mktemp`/`cat`/`rm`. |
| `api-docs-check` | `api-docs-check` | Same. |
| `docs` | `docs` | Same, calls `go run ./hack/docgen ...` four times. |
| `docs-check` | `docs-check` | Same. |
| `build` | `build` | Dropped explicit `vet` duplication (folded into `typecheck` dependency); kept `fmt` (build, unlike test/check, is allowed to mutate). |
| `run` | `run` | Marked long-running per §2. |
| `docker-buildx-setup` | `_docker-buildx-setup` (private) | Only ever called as a dependency; not a developer-facing recipe. |
| `docker-buildx-remove` | `docker-buildx-remove` | Same. |
| `docker-build` | `docker-build` | **Added `[confirm]`** — pushes to a registry, per §5.4. |
| `docker-build-load` | `docker-build-load` | Local-only, no push — no confirm. |
| `docker-build-mock-api` | `docker-build-mock-api` | Same. |
| `docker-push` | *(dropped)* | Was `$(MAKE) docker-build` with an echo — pure alias for a recipe that already pushes. No just recipe needed; if a caller used `make docker-push`, they now run `just docker-build`. |
| `docker-buildx` | *(dropped)* | Makefile already marked this "(deprecated, use docker-build)" and aliased to it. Not carried forward. |
| `build-installer` | `build-installer` | Same. |
| `install` | `install` | Same. |
| `uninstall` | `uninstall` | **Added `[confirm]`** — deletes cluster resources. Dropped the `ignore-not-found=$(ignore-not-found)` make-variable-as-flag pattern; hardcoded `--ignore-not-found=true` (matches the make default of `false`... **check this**: Makefile default is `ignore-not-found = false`, i.e. by default `kubectl delete` DOES fail on not-found. This justfile recipe hardcodes `=true`, a deliberate behavior change to make repeated `just uninstall` runs idempotent per §5.9. Flag it during review; revert to `=false` if the team wants make's original default. |
| `deploy` | `deploy` | **Added `[confirm]`** — mutates a live cluster. `HELM_VALUES`/`HELM_ARGS` now read from env (`${HELM_VALUES:+...}` / `${HELM_ARGS:-}`) instead of make variables — set them as shell env before invoking, e.g. `HELM_VALUES=my-values.yaml just deploy`. |
| `undeploy` | `undeploy` | **Added `[confirm]`**. |
| `kustomize` / `$(KUSTOMIZE)` | folded into `_install-tool` calls inside `setup` | No standalone recipe; `setup` is idempotent so re-running it is cheap. |
| `controller-gen` / `$(CONTROLLER_GEN)` | folded into `setup` | Same. |
| `setup-envtest` | `_install-envtest` (private) + used directly inside `test`/`test-e2e` | Folded — the Makefile's `setup-envtest` target both installed the `setup-envtest` binary (via its `envtest` prereq) and then ran `use` to fetch K8s binaries; the just `test` recipe does both inline. |
| `envtest` / `$(ENVTEST)` | `_install-envtest` (private) | Same version computation as `ENVTEST_VERSION` (dynamic, via `go list`). |
| `golangci-lint` / `$(GOLANGCI_LINT)` | folded into `setup` | Same pattern as kustomize/controller-gen. |
| `helm-docs` / `$(HELM_DOCS)` | folded into `setup` | Same. |
| `crd-ref-docs` / `$(CRD_REF_DOCS)` | folded into `setup` | Same. |
| `go-install-tool` (define) | `_install-tool` (private `[script('bash')]` recipe) | Same versioned-symlink idempotency check, parameterised instead of a make `define`/`call`. |
| `gomodver` (define) | inlined into the `envtest_version`/`envtest_k8s_version` top-level var computation | Same `go list -m -f '{{if .Replace}}...{{end}}'` logic; see trap #1 for why this is safe as written. |

**After `just check` and `just ci` both pass locally against this justfile, and CI (§5) is switched
over and green: `git rm Makefile`.**

## 4. Script disposition

Only one tracked shell script in the repo (excluding `vendor/`/`node_modules/`/`third_party/`/`.venv/`, none of which exist here as directories with scripts anyway):

| Script | Disposition | Reason / recipe |
|---|---|---|
| `.devcontainer/post-install.sh` | **KEEP** | Devcontainer bootstrap: installs `kind`, `kubebuilder`, `kubectl` onto a fresh devcontainer image and waits for Docker — runs via `.devcontainer/devcontainer.json`'s `postCreateCommand` (or equivalent) on a machine that has no `just` yet, since `just` itself isn't guaranteed present before this script runs. Per §6 of the standard this is a shipped bootstrap script invoked by something other than a developer typing commands (the devcontainer lifecycle), so it stays a file, untouched. No recipe wraps it — nothing to wrap; it is the thing that makes `just setup` runnable in the first place. |

`hack/docgen/*.go` (main.go, common.go, flags.go, metrics.go, events.go, samples.go, conditions.go,
stubs.go, flags_test.go) is a real Go program (compiled and run via `go run ./hack/docgen`), not a
task script — out of scope for migration, called from `docs`/`docs-check` exactly as the Makefile
called it.

## 5. CI changes

### `.github/workflows/ci.yaml`

Every job that currently checks out code and sets up Go needs a `setup-just` step inserted right
after the `actions/setup-go` step (or after checkout if the job has no Go step). Exact insertion:

```yaml
      - name: Set up just
        uses: extractions/setup-just@<RESOLVE-AT-IMPLEMENTATION-SHA> # v3
        with:
          just-version: '1.58.0'
```

`<RESOLVE-AT-IMPLEMENTATION-SHA>` is deliberately not filled in — resolve the current
`extractions/setup-just` release SHA at implementation time (do not carry forward a guessed or
stale SHA) and pin it with a `# v<major>` trailing comment, matching this repo's existing SHA-pin
convention (see `actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1` in the same
file for the style).

Per-job step edits:

- **`lint` job** — replace:
  ```yaml
        - name: Verify go.mod/go.sum are tidy
          run: |
            go mod tidy
            git diff --exit-code -- go.mod go.sum || (echo "go.mod/go.sum are not tidy. Run 'go mod tidy' and commit the result." && exit 1)

        - name: Run lint
          run: make lint
  ```
  with (after inserting the setup-just step, and a `just setup` step before both — golangci-lint
  needs to be on disk):
  ```yaml
        - name: Install dev tools
          run: just setup

        - name: Run lint
          run: just lint
  ```
  (`just lint` now runs `mod-tidy-check` as a dependency, so the separate tidy step folds in —
  don't keep both.)

- **`test` job** — replace `run: make test` with `run: just test` (after inserting `setup-just` +
  `just setup` steps — `test` needs `envtest`/`controller-gen` on `PATH` via `bin/`). Keep the
  `Download dependencies` (`go mod download`) step as-is; keep both coverage-upload steps as-is
  (`cover.out` path is unchanged).

- **`build` job** — replace:
  ```yaml
        - name: Build
          run: make build

        - name: Generate manifests
          run: make manifests

        - name: Check for uncommitted changes
          run: |
            git diff --exit-code || (echo "Uncommitted changes detected. Run 'make manifests generate' and commit." && exit 1)
  ```
  with:
  ```yaml
        - name: Install dev tools
          run: just setup

        - name: Build
          run: just build

        - name: Verify generated code is committed
          run: just gen-check
  ```

- **`verify-installer` job** — replace `Regenerate install.yaml` (`make build-installer`) +
  `Verify install.yaml is committed` (`git diff --exit-code -- dist/install.yaml`) with
  `just setup` then `run: just installer-check` (single step; `installer-check` already runs
  `build-installer` as a dependency and does the diff check).

- **`verify-docs` job** — replace `run: make docs-check` with `just setup` then `run: just
  docs-check`.

- **`verify-helm` job** — this job does NOT set up Go/just tools other than `azure/setup-helm`.
  Replace the four `run:` blocks (lint chart, template chart, NetworkPolicy render gate, chart
  README check) with a single `run: just helm-lint` for the first three checks, and keep
  `chart-docs-check` separate since it needs `helm-docs` from `bin/` (requires Go + `just setup`):
  ```yaml
        - name: Set up Go
          uses: actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e # v7.0.0
          with:
            go-version-file: go.mod
            cache: true

        - name: Set up just
          uses: extractions/setup-just@<RESOLVE-AT-IMPLEMENTATION-SHA> # v3
          with:
            just-version: '1.58.0'

        - name: Install dev tools
          run: just setup

        - name: Lint and template Helm chart
          run: just helm-lint

        - name: Verify chart README is regenerated from values.yaml
          run: just chart-docs-check
  ```
  (adds a Go setup step this job didn't previously have — needed because `chart-docs-check` now
  goes through `bin/helm-docs`, same tool the Makefile already required here transitively).

- **`ci-success` job** — **do not touch.** `needs: [lint, test, build, verify-installer,
  verify-docs, verify-helm]` and the skip/failure/cancelled logic stay exactly as-is; this is the
  branch-ruleset gate.

Preserve everywhere: `permissions: contents: read`, the `concurrency:` block, every
`persist-credentials: false`, every SHA pin + trailing `# vX.Y.Z` comment on the actions already
in this file.

### `.github/workflows/test.yml`

Standalone `Tests` workflow, functionally a duplicate of `ci.yaml`'s `test` job (pre-existing
redundancy in the repo — not this task's job to remove, just migrate faithfully). Replace:

```yaml
      - name: Running Tests
        run: |
          go mod tidy
          make test
```

with (after inserting `setup-just` + `just setup` steps following the existing `Setup Go` step):

```yaml
      - name: Set up just
        uses: extractions/setup-just@<RESOLVE-AT-IMPLEMENTATION-SHA> # v3
        with:
          just-version: '1.58.0'

      - name: Install dev tools
        run: just setup

      - name: Running Tests
        run: |
          go mod tidy
          just test
```

Preserve `concurrency:`, `permissions: contents: read`, `persist-credentials: false`.

### `.github/workflows/e2e.yaml`

Replace:

```yaml
      - name: Run E2E tests
        run: make test-e2e
        env:
          KIND_CLUSTER: fm-crd-e2e
        timeout-minutes: 15
```

with (insert `setup-just` + `just setup` after the existing `Set up Go` step; the Kind cluster is
already created by the `helm/kind-action` step above with `cluster_name: fm-crd-e2e`, so
`just test-e2e`'s own `setup-test-e2e` dependency correctly no-ops — it checks `kind get clusters`
for a cluster of that name before creating one):

```yaml
      - name: Set up just
        uses: extractions/setup-just@<RESOLVE-AT-IMPLEMENTATION-SHA> # v3
        with:
          just-version: '1.58.0'

      - name: Install dev tools
        run: just setup

      - name: Run E2E tests
        run: just test-e2e
        env:
          KIND_CLUSTER: fm-crd-e2e
        timeout-minutes: 15
```

Note: `just test-e2e` also runs `just cleanup-test-e2e` at the end, which carries `[confirm]`.
**This will hang the CI job** (stdin is not `/dev/null` in a `run:` step by default — actually
GitHub Actions runs steps with stdin closed, which per §10 of the standard makes `[confirm]` fail
closed at exit 1, not hang — so `just test-e2e` will fail at the cleanup step in CI even after
tests pass). Fix before wiring this in: split `cleanup-test-e2e` out of `test-e2e`'s body so CI
doesn't run it (the CI Kind cluster is torn down by the runner exiting anyway, or by
`helm/kind-action`'s own lifecycle), OR give CI its own recipe. **Simplest fix**: remove the
`@just cleanup-test-e2e` line from `test-e2e`'s body in the justfile above before filing this —
local developers who want cleanup already have `just cleanup-test-e2e` as a standalone recipe.
Apply that fix to the justfile in §2 as part of implementing this task (delete the line
`@just cleanup-test-e2e` from the `test-e2e` recipe body). This is called out again in Traps (§9,
trap #4) — do not miss it.

Preserve: `Create Kind cluster` step, `Verify cluster` step, all `if: failure()` log-collection
steps, `concurrency:`, `permissions: contents: read`, `persist-credentials: false`.

### Every other workflow — out of scope, do not touch

`actionlint.yml`, `arm-automerge.yml`, `auto-rc.yml`, `codeql.yml`, `dependency-review.yml`,
`docker-security.yml`, `ghcr-cleanup.yml`, `publish.yml`, `release-please.yaml`, `scorecard.yml`,
`trigger-docs-sync.yml`, `zizmor.yml` — none reference `make` or a repo script (verified by grep).
`publish.yml` does call `make build-installer` inside its `kustomize-installer` job (line ~54:
`make build-installer IMG="${IMAGE_REPO}:${RELEASE_TAG#v}"`). **This one line is in scope** — it's
not GitHub-native logic, it's a build command. Replace:

```yaml
        run: |
          make build-installer IMG="${IMAGE_REPO}:${RELEASE_TAG#v}"
          mv dist/install.yaml "dist/install-${RELEASE_TAG}.yaml"
          gh release upload "${RELEASE_TAG}" "dist/install-${RELEASE_TAG}.yaml" --clobber
```

with (needs `just` on `PATH` — add a `setup-just` step to the `kustomize-installer` job, after its
existing `actions/setup-go` step; no `just setup` needed here since `build-installer` only needs
`kustomize`, so run `just _install-tool kustomize sigs.k8s.io/kustomize/kustomize/v5 v5.7.1`
directly, or just call the full `just setup` — simpler, slightly slower):

```yaml
      - uses: extractions/setup-just@<RESOLVE-AT-IMPLEMENTATION-SHA> # v3
        with:
          just-version: '1.58.0'
      - run: just setup
      - env:
          GH_TOKEN: ${{ secrets.GITHUB_TOKEN }}
          RELEASE_TAG: ${{ inputs.release_tag }}
          IMAGE_REPO: ghcr.io/${{ github.repository }}
        run: |
          IMG="${IMAGE_REPO}:${RELEASE_TAG#v}" just build-installer
          mv dist/install.yaml "dist/install-${RELEASE_TAG}.yaml"
          gh release upload "${RELEASE_TAG}" "dist/install-${RELEASE_TAG}.yaml" --clobber
```

Do not touch the `uses: rknightion/.github/.github/workflows/container-publish.yml@...` reusable
call in the `image` job, or any `permissions:` block in this file.

## 6. Docs and agent-contract changes

- **`CONTRIBUTING.md`** — every `make <target>` reference becomes `just <recipe>` per the table in
  §3. Specifically: line 40 `make install-tools` → `just setup` (note: `install-tools` was never a
  real Makefile target — this line was already stale/broken; `just setup` is the correct replacement
  and actually exists). Lines 71/74/87/90/104/107/114/117/124/128/187/196/203/215/221/231/234/
  290/340-347/401-424: mechanical `make X` → `just X` per the table (`make docker-build-load
  IMG=...` → `IMG=... just docker-build-load`; `make docker-build IMG=...` → `IMG=... just
  docker-build` — flag that this now prompts `[confirm]` in an interactive shell). Also update the
  "Prerequisites" list (~line 20): drop `make`, add `just`.
- **`README.md`** — line 252 (`generated from values.yaml via `make chart-docs``) → `just
  chart-docs`; line 263 (`updated by `make docs`; CI fails the...`) → `just docs`.
- **`AGENTS.md`** — replace the "Common Commands" block (lines ~65-101) with `just`-equivalents per
  the table in §3, AND add the fleet-standard "Task interface" section from §9 of the frozen
  standard verbatim (adjust only the repo name if the template says one):
  ```markdown
  ## Task interface

  This repo's task surface is a `justfile`. Discover it, don't guess it:

      just --list                        # human-readable
      just --dump --dump-format json     # machine-readable
      just --show <recipe>               # what a recipe actually runs

  - `just check` is the full gate and is exactly what CI enforces. It must pass before you commit.
  - Prefer `just <recipe>` over the underlying tool. If you are typing `pytest`, you want `just test`.
  - Run `just` with stdin from /dev/null. Recipes marked `[confirm]` are destructive — stop and ask
    before running one; never pass `--yes` or `JUST_YES=1`.
  - If a task you need does not exist, add a recipe with a `#` doc comment and a `[group(...)]`
    rather than running a bare command.
  ```
  Also line 277 (`by `make docs`. Do not maintain the table by hand here.`) → `just docs`.
  `CLAUDE.md` imports `AGENTS.md` (per this repo's own note at the top of `AGENTS.md` — "Claude Code
  reads it through the `@AGENTS.md` import in `CLAUDE.md`") so no separate `CLAUDE.md` edit is
  needed for the Common Commands / Task interface content; grep `CLAUDE.md` for any independent
  `make` reference before finishing and fix any found.
- **`charts/fleet-management-operator/CLAUDE.md`** — grep it for `make` too; not read during this
  analysis pass, check before closing this task.

## 7. `backlog/config.yml`

Current:

```yaml
definition_of_done:
  - "make lint"
  - "make test"
  - "make manifests generate && git diff --exit-code (only if CRD types or RBAC markers changed)"
  - "make docs-check && make chart-docs-check (only if docs/ or chart values changed)"
```

New:

```yaml
definition_of_done:
  - "just lint"
  - "just test"
  - "just gen-check (only if CRD types or RBAC markers changed)"
  - "just docs-check (only if docs/ or chart values changed — includes chart-docs-check)"
```

Edit via `backlog config.yml` hand-edit — this is the one file the repo's own `AGENTS.md` says may
be hand-edited directly (list-valued keys can't go through `backlog config set`). Do not touch any
other key.

## 8. Order of work

1. Add `justfile` at repo root (content in §2), with the `test-e2e` fix from §5 applied (no
   `@just cleanup-test-e2e` line in its body).
2. Run `just setup` locally. Confirm all six tools land in `bin/` and are executable.
3. Run `just fmt-check`, `just lint`, `just typecheck`, `just test`, `just gen-check`, `just
   installer-check`, `just docs-check`, `just helm-lint`, `just chart-docs-check` individually.
   Fix anything the justfile got wrong about a real command (this is the step where drift between
   this task's assumptions and the live repo gets caught — the repo may have moved since this task
   was filed).
4. Run `just check` end to end. Must be green.
5. Run `just --fmt --check`. Must be clean (run `just --fmt` once if not, then re-verify
   `fmt-check`).
6. Run `just --list` and eyeball it — every public recipe needs a doc comment and a group; `just
   --groups` should show exactly `check build dev gen infra release` (release will be empty/unused
   here — that's fine, it's optional and this repo doesn't need a `release` recipe since
   release-please handles versioning).
7. Optionally, if a Kind/Docker environment is available, run `just test-e2e` locally (creates and
   tears down `fm-crd-test-e2e`).
8. Switch CI: apply the edits in §5 to `ci.yaml`, `test.yml`, `e2e.yaml`, `publish.yml`. Push and
   watch `ci-success` go green on a real PR before merging — do not merge on faith.
9. Update docs and the agent contract (§6).
10. Update `backlog/config.yml` (§7).
11. Only once CI is green on the `just`-based workflows and nothing in the tree references
    `make` or the old Makefile: `git rm Makefile`, commit.

Justfile first, prove it locally. CI second. Deletion last.

## 9. Traps specific to this repo

1. **Go-template `{{ }}` inside a top-level `:=` backtick assignment is safe; inside a recipe body
   it is not.** The `envtest_version`/`envtest_k8s_version` computation in §2 embeds a literal
   `go list -m -f '{{if .Replace}}...{{end}}'` format string. This works because top-level `:=`
   backtick assignments are evaluated as raw shell text at parse time — just's own `{{recipe-body
   interpolation}}` only applies inside indented recipe body lines. Verified locally with `just
   1.58.0`: a top-level `x := \`go list -f '{{if .Replace}}...{{end}}' pkg\`` parses and runs
   correctly; the same text pasted into a recipe body does not (just tries to interpolate `{{if
   .Replace}}` as a variable reference and fails, or mangles the braces). **Do not move this
   computation into `_install-envtest` or any recipe body without rewriting it as plain bash
   without Go-template braces** (e.g. `go list -m sigs.k8s.io/controller-runtime` plus separate
   parsing, dropping the `.Replace` handling — acceptable simplification, but note the Makefile's
   original handles the `replace` directive in `go.mod` and a naive `go list -m` does not always).
2. **`test`'s Makefile prerequisite chain included `fmt`, which mutates.** The fleet contract
   (§1 of the standard) forbids `test` mutating tracked files. The justfile above deliberately
   drops `fmt` from `test`'s (and `test-e2e`'s) dependency list, keeping only `gen` (manifests +
   generate, which only touch generated files that are supposed to be committed and checked via
   `gen-check` — not test-time mutation of source) and `typecheck` (vet, read-only). If CI ever
   diverges because `test` used to also silently reformat code before running, that divergence is
   intentional — don't "fix" it by adding `fmt` back.
3. **`ENVTEST_K8S_VERSION`/`ENVTEST_VERSION` are computed from `go.mod`, not hardcoded.** If
   `sigs.k8s.io/controller-runtime` or `k8s.io/api` ever get a `replace` directive with no
   resolvable version (Makefile's own comment: "controller-runtime replace has no tag"), both the
   Makefile and this justfile fail loudly with a clear message rather than silently using a wrong
   envtest version. Preserve that fail-closed behavior — don't add a fallback default version.
4. **`test-e2e` must not call `cleanup-test-e2e` when run from CI** — `[confirm]` fails closed on
   closed stdin (which is correct and desired for a human running it accidentally), but a `run:`
   step in Actions also has non-interactive stdin, so an un-fixed `test-e2e` recipe would fail
   *after* tests pass, turning a green e2e run red. Fixed in §2/§5 by dropping the
   `@just cleanup-test-e2e` line from `test-e2e`'s body — confirm this fix is actually applied
   before wiring `e2e.yaml` to call `just test-e2e`, not just documented here.
5. **`kind_cluster` differs between local dev and CI.** Makefile hardcodes `KIND_CLUSTER ?=
   fm-crd-test-e2e`; `e2e.yaml` overrides it to `fm-crd-e2e` via the `KIND_CLUSTER` env var, and
   `helm/kind-action` also creates a cluster under that same CI-only name. The justfile's
   `kind_cluster := env("KIND_CLUSTER", "fm-crd-test-e2e")` preserves both: local runs get the
   original default, CI's `env: KIND_CLUSTER: fm-crd-e2e` step in `e2e.yaml` overrides it so
   `setup-test-e2e`'s own `kind get clusters` check correctly finds the cluster `helm/kind-action`
   already created and no-ops instead of creating a second one.
6. **`install`/`uninstall`/`deploy` assume a `kubectl`/`helm` context already points at the
   intended cluster** (same assumption the Makefile made — `~/.kube/config` current context).
   `just` does not add any safety net here beyond the `[confirm]` prompts already added; it does
   not check which cluster context is active. Out of scope to add — flag if asked, don't
   silently add cluster-context validation logic that wasn't in the Makefile.
7. **`golangci-lint` is v2** (`.golangci.yml` has `version: "2"`, package path is
   `.../golangci-lint/v2/cmd/golangci-lint`). Don't accidentally pin the v1 module path when
   filling in `_install-tool` — v1's binary CLI surface differs (`run` vs `run ./...` flag
   handling, `config verify` subcommand availability).
8. **`docker-build`'s `[confirm]` changes the interactive contract from the Makefile.** The old
   `make docker-build` pushed immediately with no prompt. An agent following AGENTS.md's "never
   pass `--yes`" rule will now stop and ask before pushing an image — this is intentional per the
   fleet standard (§5.4: confirm on anything that mutates a remote) and is a deliberate behavior
   change from the Makefile, not a bug to "fix" back.
9. **`uninstall`'s `--ignore-not-found` default flipped from `false` to `true`** (see §3 table,
   `uninstall` row) to make repeated invocations idempotent per §5.9 of the standard. This is a
   silent behavior change worth flagging in the PR description — revert to reading an
   `IGNORE_NOT_FOUND` env var defaulting to `false` if code review wants to preserve the exact old
   default.

## 10. Out of scope

Do not touch, in this task:

- `.devcontainer/post-install.sh` (KEEP, §4).
- `hack/docgen/*.go` — real Go program, called as-is from `docs`/`docs-check`.
- Any workflow not named in §5: `actionlint.yml`, `arm-automerge.yml`, `auto-rc.yml`, `codeql.yml`,
  `dependency-review.yml`, `docker-security.yml`, `ghcr-cleanup.yml`, `release-please.yaml`,
  `scorecard.yml`, `trigger-docs-sync.yml`, `zizmor.yml` — all GitHub-native, no `make`/script
  content.
- The `container-publish.yml` reusable workflow call inside `publish.yml`'s `image` job.
- `release-please` auth/config — this repo follows the fleet's OpenBao-broker release-please setup;
  nothing here changes it.
- Consolidating `test.yml` and `ci.yaml`'s duplicate test job — pre-existing redundancy, not this
  task's job (both get migrated to call `just test`, faithfully preserving the duplication).
- Any `backlog/config.yml` key other than `definition_of_done`.
- `charts/fleet-management-operator/values.yaml`, chart templates, or any Helm chart content beyond
  what `just helm-lint`/`chart-docs`/`chart-docs-check` already exercised via the Makefile.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A top-level justfile exists with default, setup, fmt, fmt-check, lint, test, check plus typecheck, build, run, gen, gen-check, docs, ci, all with a doc comment and [group(...)]
- [ ] #2 just check passes locally and matches exactly what ci.yaml's ci-success job gates on (lint, test, build/gen-check, verify-installer/installer-check, verify-docs/docs-check, verify-helm/helm-lint+chart-docs-check)
- [ ] #3 just --fmt --check passes
- [ ] #4 just --list shows a doc comment and a group for every public recipe, and just --groups shows exactly check build dev gen infra release
- [ ] #5 Makefile is deleted (git rm) only after CI is green on the just-based workflows
- [ ] #6 .devcontainer/post-install.sh is untouched (KEEP, devcontainer bootstrap script)
- [ ] #7 ci.yaml, test.yml, e2e.yaml, and publish.yml's kustomize-installer job call just recipes instead of make targets, with a setup-just step inserted per job, and ci-success's needs list and skip/failure/cancelled logic are unchanged
- [ ] #8 CONTRIBUTING.md, README.md, and AGENTS.md no longer reference any make target, and AGENTS.md carries the fleet-standard Task interface section
- [ ] #9 backlog/config.yml's definition_of_done names just lint, just test, just gen-check, and just docs-check instead of make targets
- [ ] #10 test-e2e does not invoke cleanup-test-e2e in its own body (would hang/fail closed under [confirm] on CI's non-interactive stdin)
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 make lint
- [ ] #2 make test
- [ ] #3 make manifests generate && git diff --exit-code (only if CRD types or RBAC markers changed)
- [ ] #4 make docs-check && make chart-docs-check (only if docs/ or chart values changed)
<!-- DOD:END -->

## Comments

<!-- COMMENTS:BEGIN -->
author: campaign-ordering
created: 2026-08-29 09:18
---
## Fleet ordering — WAVE 2. Starts after the Wave 0 pilot (`sf2loki` / SFL-0073) and the Wave 1 hubs land.

Within Wave 2 the order is free — these repos do not depend on each other. Batching by language is worthwhile so one lane reuses its Makefile-to-recipe mapping across similar repos.

Do not start before the pilot reports. The standard may be amended off the back of it, and picking this up early risks coding against a superseded seam.

**Provisioning `just` in CI.** Which mechanism depends on the runner, and the two must not be mixed:

| Runner | Mechanism |
| --- | --- |
| `arc-arm64` (m7kni self-hosted) | `just` is **baked into the runner image** by `m7kni/ci-tools` (`runner-image/Dockerfile`, `ARG JUST_VERSION`). Do **not** add `extractions/setup-just`, and delete the step if this repo already has one — it installs a second `just` earlier on `PATH` and turns the image pin into a lie. |
| GitHub-hosted (all `rknightion` repos) | `extractions/setup-just`, SHA-pinned, with an explicit `just-version:`. |

Both sides currently sit on **1.58.0** and are Renovate-managed. `ci-tools`' `Tool version drift` workflow fails if the Dockerfile `ARG` and the published image ever disagree, and lists any repo still carrying a second pin.

**While you are in the workflow files, check the hub pin.** On 2026-08-29 Renovate was unfrozen for `rknightion/.github` in `m7kni/renovate-config` — it had been `enabled: false` on the mistaken belief that callers tracked `@main`, which froze the fleet across 19 different hub SHAs (v1.3.1 June → v1.9.7 August) so that no hub fix ever propagated. Bumps now arrive as one grouped, CI-gated, automerged PR per repo. **A `uses:` whose comment is not a real `# vX.Y.Z` still cannot be bumped** (it resolves to a digest-only update, which the fleet rules disable) — if you find one, repair the comment as part of this task.
---

author: campaign-ordering
created: 2026-08-29 10:42
---
## Standard amendment — `ci` is the sanctioned superset of `check` (RATIFIED)

This supersedes the frozen wording *"`check` is the complete local gate and reproduces every CI job that can run off a GitHub runner"*, which several lanes could not honour without making the pre-commit gate depend on a Docker daemon.

**The definitions now are:**

- **`check`** — everything that runs with **only the language toolchain installed**. This is the pre-commit gate. A leg that runs on a bare toolchain belongs here *however long it takes*.
- **`ci`** — `check` plus the legs CI gates that need a **Docker daemon, a service container, or cross-compilation**, and nothing else. Written as `ci: check <heavy legs>`.

**Every leg you put in `ci` must carry a comment naming which of those three it needs.** That comment is the guard: without it `ci` becomes the bin for anything slow or awkward, `check` quietly stops meaning much, and the fleet is back to a per-repo gate.

Eleven of the 42 lanes arrived at this shape independently before it was ratified, which is why it won.

**If this repo has no such legs, it has no `ci` recipe at all** and `check` is the whole gate. Do not add an empty one.
---
<!-- COMMENTS:END -->
