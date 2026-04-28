# Invalid Sample CRs

These manifests are intentionally invalid. They exist so users hitting an
admission failure can compare their manifest against a documented
counter-example and see the exact error message produced. Each file
includes a header comment naming the rule it violates and the source of
that rule (CEL on the API server, or Go-side webhook validator).

**Do not** apply these in CI or production:

```bash
# All of these will fail at admission. That is the point.
kubectl apply -f config/samples/invalid/

# Try one to see the error message:
kubectl apply -f config/samples/invalid/pipeline_invalid_matcher_syntax.yaml
```

## Files

| File                                              | Violation                                                              | Enforced by    |
| ------------------------------------------------- | ---------------------------------------------------------------------- | -------------- |
| `pipeline_invalid_matcher_syntax.yaml`            | Uses `==` instead of `=` in a matcher.                                 | webhook        |
| `pipeline_invalid_oversized_matcher.yaml`         | Single matcher exceeds 200 characters.                                 | API server (OpenAPI `items.maxLength`) |
| `collector_invalid_reserved_key.yaml`             | `spec.remoteAttributes` key uses the reserved `collector.` prefix.     | API server (CEL) + webhook |
| `collector_invalid_id_change.yaml` *(see below)*  | Mutating `spec.id` after creation.                                     | API server (CEL `self == oldSelf`) + webhook |
| `policy_invalid_empty_selector.yaml`              | `spec.selector` has neither matchers nor collectorIDs.                 | webhook        |
| `tenant_policy_invalid_matcher_syntax.yaml`       | `spec.requiredMatchers` uses `==`.                                     | webhook        |

## How to reproduce the immutability error (`collector_invalid_id_change.yaml`)

```bash
kubectl apply -f config/samples/collector_sample.yaml
# Now edit the file: change spec.id, then apply:
kubectl apply -f config/samples/invalid/collector_invalid_id_change.yaml
# Expected: API server rejects with "spec.id is immutable" (CEL rule).
```

## Why these aren't in `kustomization.yaml`

`config/samples/kustomization.yaml` is consumed by tooling that batch-
applies samples. Adding an invalid manifest there would break those
flows. Invalid samples are referenced individually for troubleshooting
and onboarding only.
