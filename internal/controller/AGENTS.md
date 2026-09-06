# internal/controller

## pipeline_controller.go is shape-pinned by AST audit tests

`cache_audit_test.go`, `reconcile_audit_test.go` and `watch_audit_test.go` parse this file (and
`../../cmd/main.go`) instead of exercising it, so an ordinary refactor fails them with an error that
looks nothing like the change you made. What they pin:

- exactly 3 `Status().Update()` calls and exactly 2 `r.Update()` calls (finalizer add and remove)
- no `List()` call anywhere in the file, and the reconciler embeds the manager's cached
  `client.Client`
- no `WithOptions(...)` in `SetupWithManager`
- exactly 5 `Reconcile:` comment markers, one per Kubernetes API call, plus the package-level
  `Reconcile Loop Audit:` and `Cache Usage Audit:` summaries and a `Cache:` marker on each call.
  The full marker strings are asserted verbatim; the tests carry the list.

Adding or removing an API call means updating the counts and the marker comments in the same edit.

## Retry classification

`errors.go` owns it: `isTransientError` and `shouldRetry`. Classify through those rather than
inspecting the error at the call site, so rate-limit, validation and Fleet API errors keep their
distinct requeue behaviour.

## Tests

`just test` installs setup-envtest and exports `KUBEBUILDER_ASSETS`. A bare `go test` in this
package cannot start the control plane. `just test <filter>` maps the filter to `go test -run`.

## Deeper references

- `reference/attributes-and-discovery.md` - single-writer rule for `BulkUpdateCollectors`,
  attribute precedence, the deliberate absence of an `ObservedGeneration` guard on the Collector
  reconciler, and the discovery naming, tracking and pagination behaviour.
