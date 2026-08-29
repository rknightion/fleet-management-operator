set shell := ["bash", "-euo", "pipefail", "-c"]

# renovate: datasource=github-releases depName=kubernetes-sigs/kustomize versioning=semver
kustomize_version := "v5.7.1"
# renovate: datasource=github-releases depName=kubernetes-sigs/controller-tools versioning=semver
controller_tools_version := "v0.20.0"
# renovate: datasource=github-releases depName=golangci/golangci-lint versioning=semver
golangci_lint_version := "v2.13.1"
# renovate: datasource=github-releases depName=norwoodj/helm-docs versioning=semver
helm_docs_version := "v1.14.2"
# renovate: datasource=github-releases depName=elastic/crd-ref-docs versioning=semver
crd_ref_docs_version := "v0.3.0"

# controller-runtime's resolved version drives the envtest release branch; k8s.io/api's
# resolved version drives the envtest Kubernetes version. These are evaluated at parse time,
# so the Go template braces are not subject to recipe interpolation.
envtest_version := `v="$(go list -m -f '{{if .Replace}}{{.Replace.Version}}{{else}}{{.Version}}{{end}}' sigs.k8s.io/controller-runtime 2>/dev/null || true)"; [ -n "$v" ] || { echo "Set envtest_version manually (controller-runtime replace has no tag)" >&2; exit 1; }; printf '%s\n' "$v" | sed -E 's/^v?([0-9]+)\.([0-9]+).*/release-\1.\2/'`
envtest_k8s_version := `v="$(go list -m -f '{{if .Replace}}{{.Replace.Version}}{{else}}{{.Version}}{{end}}' k8s.io/api 2>/dev/null || true)"; [ -n "$v" ] || { echo "Set envtest_k8s_version manually (k8s.io/api replace has no tag)" >&2; exit 1; }; printf '%s\n' "$v" | sed -E 's/^v?[0-9]+\.([0-9]+).*/1.\1/'`

localbin := justfile_directory() / "bin"
img := env("IMG", "ghcr.io/rknightion/fleet-management-operator:dev")
platforms := env("PLATFORMS", "linux/arm64,linux/amd64")
builder_name := env("BUILDER_NAME", "fm-crd-builder")
kind_cluster := env("KIND_CLUSTER", "fm-crd-test-e2e")
helm_release := env("HELM_RELEASE", "fleet-management-operator")
helm_namespace := env("HELM_NAMESPACE", "fleet-management-system")
container_tool := env("CONTAINER_TOOL", "docker")
kind_bin := env("KIND", "kind")
kubectl_bin := env("KUBECTL", "kubectl")
helm_bin := env("HELM", "helm")

# show the task surface
default:
    @just --list

# install the pinned development tools into ./bin
setup:
    just _install-tool kustomize sigs.k8s.io/kustomize/kustomize/v5 {{ kustomize_version }}
    just _install-tool controller-gen sigs.k8s.io/controller-tools/cmd/controller-gen {{ controller_tools_version }}
    just _install-tool golangci-lint github.com/golangci/golangci-lint/v2/cmd/golangci-lint {{ golangci_lint_version }}
    just _install-tool helm-docs github.com/norwoodj/helm-docs/cmd/helm-docs {{ helm_docs_version }}
    just _install-tool crd-ref-docs github.com/elastic/crd-ref-docs {{ crd_ref_docs_version }}
    just _install-envtest
    @echo "setup complete: {{ localbin }}"

# format Go sources and this justfile in place
[group('check')]
fmt:
    go fmt ./...
    just --fmt

# verify Go and justfile formatting without mutating tracked files
[group('check')]
[no-exit-message]
fmt-check:
    test -z "$(gofmt -l .)" || { gofmt -l .; echo "run 'just fmt'"; exit 1; }
    just --fmt --check

# run go vet for the module
[group('check')]
[no-exit-message]
typecheck:
    go vet ./...

# verify go.mod and go.sum are tidy
[group('check')]
[no-exit-message]
mod-tidy-check:
    go mod tidy
    git diff --exit-code -- go.mod go.sum

# run golangci-lint
[group('check')]
[no-exit-message]
lint: mod-tidy-check
    "{{ localbin }}/golangci-lint" run ./...

# apply golangci-lint auto-fixes
[group('check')]
lint-fix:
    "{{ localbin }}/golangci-lint" run --fix ./...

# verify the golangci-lint configuration
[group('check')]
[no-exit-message]
lint-config:
    "{{ localbin }}/golangci-lint" config verify

# run the unit and envtest suite; filter maps to go test -run
[group('check')]
[no-exit-message]
[script('bash')]
test filter="": gen typecheck mod-tidy-check _install-envtest
    assets="$("{{ localbin }}/setup-envtest" use {{ envtest_k8s_version }} --bin-dir "{{ localbin }}" -p path)"
    KUBEBUILDER_ASSETS="$assets" go test -race $(go list ./... | grep -v /e2e) -run '{{ filter }}' -coverprofile cover.out

# create or reuse the isolated Kind cluster used by end-to-end tests
[group('infra')]
[script('bash')]
setup-test-e2e:
    command -v "{{ kind_bin }}" >/dev/null 2>&1 || { echo "kind is not installed"; exit 1; }
    "{{ kind_bin }}" get clusters 2>/dev/null | grep -qx "{{ kind_cluster }}" || "{{ kind_bin }}" create cluster --name "{{ kind_cluster }}"

# delete the isolated Kind cluster used by end-to-end tests
[confirm('delete the e2e Kind cluster? [y/N]')]
[group('infra')]
cleanup-test-e2e:
    "{{ kind_bin }}" delete cluster --name "{{ kind_cluster }}"

# run end-to-end tests against an isolated Kind cluster; it needs a Docker daemon for Kind
[group('check')]
test-e2e: setup-test-e2e gen typecheck
    KIND="{{ kind_bin }}" KIND_CLUSTER="{{ kind_cluster }}" go test -tags=e2e ./test/e2e/ -v -ginkgo.v

# regenerate CRD manifests, RBAC, and webhook configuration
[group('gen')]
manifests:
    "{{ localbin }}/controller-gen" rbac:roleName=manager-role crd webhook paths="./..." output:crd:artifacts:config=config/crd/bases

# regenerate DeepCopy methods
[group('gen')]
generate:
    "{{ localbin }}/controller-gen" object:headerFile="hack/boilerplate.go.txt" paths="./..."

# regenerate all committed generated Go and Kubernetes artifacts
[group('gen')]
gen: manifests generate

# fail if generated Go or Kubernetes artifacts are stale
[group('gen')]
[no-exit-message]
gen-check: gen
    git diff --exit-code -- api/v1alpha1/zz_generated.deepcopy.go config/crd/bases config/rbac/role.yaml config/webhook/manifests.yaml || { echo "generated code is stale; run 'just gen' and commit"; exit 1; }

# regenerate the Helm chart README from values.yaml
[group('gen')]
chart-docs:
    "{{ localbin }}/helm-docs" --chart-search-root charts

# verify the Helm chart README matches values.yaml
[group('check')]
[no-exit-message]
[script('bash')]
chart-docs-check:
    expected="$(mktemp)"
    trap 'rm -f "$expected"' EXIT
    "{{ localbin }}/helm-docs" --chart-search-root charts --dry-run > "$expected"
    diff -q "$expected" charts/fleet-management-operator/README.md > /dev/null 2>&1 || { echo "charts/fleet-management-operator/README.md is out of date. Run 'just chart-docs'."; exit 1; }

# regenerate docs/api-reference.md from API type documentation and CRDs
[group('gen')]
[script('bash')]
api-docs:
    tmp="$(mktemp)"
    trap 'rm -f "$tmp"' EXIT
    "{{ localbin }}/crd-ref-docs" \
        --source-path=api/v1alpha1 \
        --config=.crd-ref-docs.yaml \
        --renderer=markdown \
        --output-path="$tmp"
    cat hack/api-reference-front-matter.md "$tmp" > docs/api-reference.md

# verify docs/api-reference.md matches API type documentation and CRDs
[group('check')]
[no-exit-message]
[script('bash')]
api-docs-check:
    tmp="$(mktemp)"
    generated="$(mktemp)"
    trap 'rm -f "$tmp" "$generated"' EXIT
    "{{ localbin }}/crd-ref-docs" \
        --source-path=api/v1alpha1 \
        --config=.crd-ref-docs.yaml \
        --renderer=markdown \
        --output-path="$generated" >/dev/null
    cat hack/api-reference-front-matter.md "$generated" > "$tmp"
    diff -q "$tmp" docs/api-reference.md > /dev/null 2>&1 || { echo "docs/api-reference.md is out of date. Run 'just api-docs'."; exit 1; }

# regenerate all auto-generated documentation
[group('gen')]
docs: api-docs chart-docs
    go run ./hack/docgen flags --out docs/flags.md
    go run ./hack/docgen metrics --out docs/metrics.md
    go run ./hack/docgen events --out docs/events.md
    go run ./hack/docgen samples --out docs/samples.md

# verify all auto-generated documentation is current
[group('check')]
[no-exit-message]
docs-check: api-docs-check chart-docs-check
    go run ./hack/docgen flags --out docs/flags.md --check
    go run ./hack/docgen metrics --out docs/metrics.md --check
    go run ./hack/docgen events --out docs/events.md --check
    go run ./hack/docgen samples --out docs/samples.md --check
    go run ./hack/docgen verify-conditions docs/conditions.md

# lint and template-render the Helm chart
[group('check')]
[no-exit-message]
[script('bash')]
helm-lint:
    set_flags=(--set fleetManagement.baseUrl=https://test --set fleetManagement.username=test --set fleetManagement.password=test)
    "{{ helm_bin }}" lint charts/fleet-management-operator "${set_flags[@]}"
    "{{ helm_bin }}" template test charts/fleet-management-operator "${set_flags[@]}" > /dev/null
    "{{ helm_bin }}" template test charts/fleet-management-operator "${set_flags[@]}" > /tmp/chart-default.yaml
    if grep -q "kind: NetworkPolicy" /tmp/chart-default.yaml; then
        echo "NetworkPolicy should be disabled by default"
        exit 1
    fi
    "{{ helm_bin }}" template test charts/fleet-management-operator "${set_flags[@]}" --set networkPolicy.enabled=true > /tmp/chart-networkpolicy.yaml
    grep -q "kind: NetworkPolicy" /tmp/chart-networkpolicy.yaml

# run the full pre-commit gate, matching ci-success's local jobs
[group('check')]
check: fmt-check lint test build gen-check installer-check docs-check helm-lint

# run the CI superset; test-e2e needs a Docker daemon for Kind
[group('check')]
ci: check test-e2e

# build the manager binary
[group('build')]
build: gen fmt typecheck
    go build -o bin/manager cmd/main.go

# run the controller locally against the current kubeconfig
[group('dev')]
run: gen fmt typecheck
    go run ./cmd/main.go

# remove the local multi-architecture buildx builder
[group('build')]
docker-buildx-remove:
    "{{ container_tool }}" buildx inspect "{{ builder_name }}" >/dev/null 2>&1 && "{{ container_tool }}" buildx rm "{{ builder_name }}" || true

# build and push a multi-architecture manager image
[confirm('push a multi-architecture image to a registry? [y/N]')]
[group('release')]
docker-build: _docker-buildx-setup
    "{{ container_tool }}" buildx build --builder "{{ builder_name }}" --platform="{{ platforms }}" --tag "{{ img }}" --push .

# build a manager image for the local architecture and load it into Docker
[group('build')]
docker-build-load:
    "{{ container_tool }}" buildx build --tag "{{ img }}" --load .

# build the mock Fleet Management API image used by end-to-end tests
[group('build')]
docker-build-mock-api:
    "{{ container_tool }}" build -t mock-fleet-api:test test/mockapi/

# regenerate dist/install.yaml from CRDs and the deployment configuration
[group('build')]
[script('bash')]
build-installer: gen
    mkdir -p dist
    cd config/manager
    "{{ localbin }}/kustomize" edit set image controller="{{ img }}"
    cd ../..
    "{{ localbin }}/kustomize" build config/default > dist/install.yaml

# verify dist/install.yaml is committed and current
[group('check')]
[no-exit-message]
installer-check: build-installer
    git diff --exit-code -- dist/install.yaml || { echo "dist/install.yaml is out of date. Run 'just build-installer' and commit."; exit 1; }

# install CRDs into the cluster selected by the current kubeconfig
[group('dev')]
[script('bash')]
install: manifests
    out="$("{{ localbin }}/kustomize" build config/crd 2>/dev/null || true)"
    if [ -n "$out" ]; then echo "$out" | "{{ kubectl_bin }}" apply -f -; else echo "No CRDs to install; skipping."; fi

# remove CRDs from the cluster selected by the current kubeconfig
[confirm('delete CRDs from the current kubeconfig cluster? [y/N]')]
[group('dev')]
[script('bash')]
uninstall: manifests
    just _uninstall-crds

# deploy or upgrade the controller Helm release in the current kubeconfig
[confirm('deploy or upgrade the controller in the current kubeconfig cluster? [y/N]')]
[group('dev')]
[script('bash')]
deploy: manifests
    image_repository="$(echo "{{ img }}" | sed 's/:[^:]*$//')"
    image_tag="$(echo "{{ img }}" | sed 's/.*://')"
    args=(upgrade --install "{{ helm_release }}" charts/fleet-management-operator --namespace "{{ helm_namespace }}" --create-namespace --set "image.repository=$image_repository" --set "image.tag=$image_tag")
    if [ -n "${HELM_VALUES:-}" ]; then args+=(--values "$HELM_VALUES"); fi
    if [ -n "${HELM_ARGS:-}" ]; then args+=($HELM_ARGS); fi
    "{{ helm_bin }}" "${args[@]}"

# uninstall the controller Helm release from the current kubeconfig
[confirm('uninstall the controller Helm release? [y/N]')]
[group('dev')]
undeploy:
    "{{ helm_bin }}" uninstall "{{ helm_release }}" --namespace "{{ helm_namespace }}" 2>/dev/null || true

[private]
[script('bash')]
_install-tool bin pkg version:
    mkdir -p "{{ localbin }}"
    target="{{ localbin }}/{{ bin }}"
    versioned="$target-{{ version }}"
    if [ -x "$versioned" ] && [ "$(readlink "$target" 2>/dev/null || true)" = "{{ bin }}-{{ version }}" ]; then
        exit 0
    fi
    echo "Downloading {{ pkg }}@{{ version }}"
    rm -f "$target"
    GOBIN="{{ localbin }}" go install "{{ pkg }}@{{ version }}"
    mv "{{ localbin }}/{{ bin }}" "$versioned"
    ln -sfn "{{ bin }}-{{ version }}" "$target"

[private]
_install-envtest:
    just _install-tool setup-envtest sigs.k8s.io/controller-runtime/tools/setup-envtest {{ envtest_version }}

[private]
[script('bash')]
_uninstall-crds:
    out="$("{{ localbin }}/kustomize" build config/crd 2>/dev/null || true)"
    if [ -n "$out" ]; then echo "$out" | "{{ kubectl_bin }}" delete --ignore-not-found=true -f -; else echo "No CRDs to delete; skipping."; fi

[private]
[script('bash')]
_docker-buildx-setup:
    if "{{ container_tool }}" buildx inspect "{{ builder_name }}" >/dev/null 2>&1; then
        "{{ container_tool }}" buildx use "{{ builder_name }}"
    else
        "{{ container_tool }}" buildx create --name "{{ builder_name }}" --driver docker-container --bootstrap --use
    fi
