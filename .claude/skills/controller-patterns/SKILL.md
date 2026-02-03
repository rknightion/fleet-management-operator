---
name: controller-patterns
description: Kubernetes controller best practices and Go idioms for building production-ready controllers
---

# Kubernetes Controller Best Practices

This skill covers critical patterns and pitfalls for building production-ready Kubernetes controllers, specifically for the Fleet Management Pipeline operator.

## Critical: Cache Consistency Issues

**Problem:** Informer cache does NOT provide read-your-writes consistency.

When you create/update a resource, the cached client may return stale data immediately after:

```go
// WRONG: Assumes immediate cache update
r.Client.Create(ctx, childResource)
r.Client.Get(ctx, key, childResource) // May return NotFound or stale data!
```

**Impact on this controller:**
- After UpsertPipeline succeeds, Pipeline status update may be based on stale cache
- Finalizer logic may see stale state during deletion
- Reconciliation may trigger unnecessary API calls

### Solutions

**1. Use ObservedGeneration pattern (recommended):**
```go
// Only reconcile if spec changed
if pipeline.Status.ObservedGeneration == pipeline.Generation {
    // Spec hasn't changed, skip Fleet Management API call
    return ctrl.Result{}, nil
}

// After successful reconcile
pipeline.Status.ObservedGeneration = pipeline.Generation
```

**2. Store reconciliation results in status:**
```go
// After successful UpsertPipeline, store ID and timestamps in status
status.ID = apiResponse.ID
status.UpdatedAt = apiResponse.UpdatedAt
status.ObservedGeneration = pipeline.Generation

// Next reconciliation can compare without external API call
```

**3. Use uncached client for critical reads:**
```go
type PipelineReconciler struct {
    Client         client.Client        // Cached
    UncachedClient client.Client        // Direct API
}

// For critical operations, use uncached client
r.UncachedClient.Get(ctx, key, pipeline)
```

## Don't Block Reconcile with Long Operations

**Problem:** Long-running operations block worker goroutines and increase workqueue depth.

**Wrong pattern:**
```go
// WRONG: Blocks worker for 30+ seconds
func (r *PipelineReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    time.Sleep(30 * time.Second) // Waiting for external validation
    checkCollectorHealth()
}
```

**Correct patterns:**

**1. Return early and requeue:**
```go
// Check if pipeline was recently updated
if time.Since(status.UpdatedAt.Time) < 30*time.Second {
    // Too soon to check if applied successfully
    return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
}
```

**2. Use status conditions for async operations:**
```go
// Set "Syncing" condition immediately
meta.SetStatusCondition(&pipeline.Status.Conditions, metav1.Condition{
    Type:   "Synced",
    Status: metav1.ConditionUnknown,
    Reason: "SyncInProgress",
})

// Make API call
apiResp, err := r.fleetClient.UpsertPipeline(ctx, req)

// Update to final condition
if err != nil {
    meta.SetStatusCondition(&pipeline.Status.Conditions, metav1.Condition{
        Type:    "Synced",
        Status:  metav1.ConditionFalse,
        Reason:  "SyncFailed",
        Message: err.Error(),
    })
} else {
    meta.SetStatusCondition(&pipeline.Status.Conditions, metav1.Condition{
        Type:   "Synced",
        Status: metav1.ConditionTrue,
        Reason: "SyncSucceeded",
    })
}
```

**3. Monitor workqueue metrics:**
```bash
# Check for growing queue depth
workqueue_depth{name="pipeline"}
workqueue_work_duration_seconds{name="pipeline"}
workqueue_unfinished_work_seconds{name="pipeline"}
```

## Avoid Unnecessary API Calls

**Problem:** Calling external APIs on every reconciliation wastes resources and hits rate limits.

**Bad pattern:**
```go
// WRONG: Always calls Fleet Management API
func (r *PipelineReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    // This runs every resync period even if nothing changed!
    apiPipeline, _ := r.fleetClient.GetPipeline(ctx, status.ID)
    r.Client.Update(ctx, pipeline) // Unnecessary status update
}
```

**Good pattern:**
```go
// CORRECT: Only call API when spec changed
func (r *PipelineReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    // Skip if spec unchanged
    if pipeline.Status.ObservedGeneration == pipeline.Generation {
        return ctrl.Result{}, nil
    }

    // Only upsert when spec actually changed
    apiPipeline, err := r.fleetClient.UpsertPipeline(ctx, buildRequest(pipeline.Spec))

    // Update status only if changed
    if !equality.Semantic.DeepEqual(pipeline.Status, newStatus) {
        pipeline.Status = newStatus
        r.Client.Status().Update(ctx, pipeline)
    }
}
```

**For this controller specifically:**
- Don't call GetPipeline unless debugging (use UpsertPipeline response)
- Don't call ListPipelines on every reconcile (expensive with 3 req/s limit)
- Cache Fleet Management state in controller memory if needed
- Use observedGeneration to skip reconciliation when spec unchanged

## Return Errors, Don't Just Requeue

**Problem:** Using `Result{Requeue: true}` instead of returning errors hides problems.

**Wrong:**
```go
pipeline, err := r.fleetClient.UpsertPipeline(ctx, req)
if err != nil {
    // WRONG: Error swallowed, no backoff, no log context
    return ctrl.Result{Requeue: true}, nil
}
```

**Correct:**
```go
pipeline, err := r.fleetClient.UpsertPipeline(ctx, req)
if err != nil {
    // CORRECT: Error returned, automatic exponential backoff
    return ctrl.Result{}, fmt.Errorf("failed to upsert pipeline: %w", err)
}
```

**When to use Requeue: true:**
- External dependency not ready yet (not an error condition)
- Scheduled recheck (e.g., after 30 seconds)
- Rate limiting (use RequeueAfter for specific delay)

**When to return error:**
- API call failures
- Invalid configuration
- Unexpected conditions
- Transient errors that should retry with backoff

## Status Updates and Conflicts

**Problem:** Status updates can fail with conflicts, causing unnecessary retries.

**Critical pattern:**
```go
// Use Status().Update() not Update()
r.Client.Status().Update(ctx, pipeline)

// Handle conflicts gracefully
if err := r.Client.Status().Update(ctx, pipeline); err != nil {
    if apierrors.IsConflict(err) {
        // Resource was modified, requeue to get fresh copy
        return ctrl.Result{Requeue: true}, nil
    }
    return ctrl.Result{}, err
}

// NEVER update status in spec update - they are separate
r.Client.Update(ctx, pipeline)        // Updates spec + metadata
r.Client.Status().Update(ctx, pipeline) // Updates status only
```

## Finalizer Patterns

**Problem:** Incorrect finalizer handling can cause deadlocks or orphaned resources.

**Critical issues:**
1. Deadlock: Two resources with finalizers waiting for each other
2. Orphaned resources: Finalizer not removed after cleanup
3. Duplicate cleanup: Finalizer logic runs multiple times

**Correct pattern:**
```go
const pipelineFinalizer = "pipeline.fleetmanagement.grafana.com/finalizer"

func (r *PipelineReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    pipeline := &Pipeline{}
    if err := r.Get(ctx, req.NamespacedName, pipeline); err != nil {
        if apierrors.IsNotFound(err) {
            return ctrl.Result{}, nil
        }
        return ctrl.Result{}, err
    }

    // Handle deletion
    if !pipeline.DeletionTimestamp.IsZero() {
        if controllerutil.ContainsFinalizer(pipeline, pipelineFinalizer) {
            // Perform cleanup
            if err := r.deleteFleetPipeline(ctx, pipeline); err != nil {
                if isNotFoundError(err) {
                    // Already deleted in Fleet Management, continue
                } else {
                    return ctrl.Result{}, err
                }
            }

            // Remove finalizer
            controllerutil.RemoveFinalizer(pipeline, pipelineFinalizer)
            if err := r.Update(ctx, pipeline); err != nil {
                return ctrl.Result{}, err
            }
        }
        return ctrl.Result{}, nil
    }

    // Add finalizer if not present
    if !controllerutil.ContainsFinalizer(pipeline, pipelineFinalizer) {
        controllerutil.AddFinalizer(pipeline, pipelineFinalizer)
        if err := r.Update(ctx, pipeline); err != nil {
            return ctrl.Result{}, err
        }
    }

    // Normal reconciliation logic
    return r.reconcileNormal(ctx, pipeline)
}

func (r *PipelineReconciler) deleteFleetPipeline(ctx context.Context, pipeline *Pipeline) error {
    if pipeline.Status.ID == "" {
        // No ID means never created in Fleet Management
        return nil
    }

    err := r.fleetClient.DeletePipeline(ctx, pipeline.Status.ID)
    if err != nil && isNotFoundError(err) {
        // Already deleted, treat as success
        return nil
    }
    return err
}
```

## Structure Reconcile Logic Consistently

**Problem:** Dumping all logic in Reconcile() makes code hard to maintain and test.

**Recommended structure:**

```go
func (r *PipelineReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    log := log.FromContext(ctx)

    // 1. Fetch resource
    pipeline := &Pipeline{}
    if err := r.Get(ctx, req.NamespacedName, pipeline); err != nil {
        return ctrl.Result{}, client.IgnoreNotFound(err)
    }

    // 2. Handle deletion
    if !pipeline.DeletionTimestamp.IsZero() {
        return r.reconcileDelete(ctx, pipeline)
    }

    // 3. Add finalizer if needed
    if !controllerutil.ContainsFinalizer(pipeline, pipelineFinalizer) {
        return r.addFinalizer(ctx, pipeline)
    }

    // 4. Check if reconciliation needed
    if pipeline.Status.ObservedGeneration == pipeline.Generation {
        return ctrl.Result{}, nil
    }

    // 5. Reconcile normal case
    return r.reconcileNormal(ctx, pipeline)
}

func (r *PipelineReconciler) reconcileNormal(ctx context.Context, pipeline *Pipeline) (ctrl.Result, error) {
    // Business logic here
    apiReq := r.buildUpsertRequest(pipeline)
    apiResp, err := r.fleetClient.UpsertPipeline(ctx, apiReq)
    if err != nil {
        return r.handleAPIError(ctx, pipeline, err)
    }

    return r.updateStatus(ctx, pipeline, apiResp)
}

func (r *PipelineReconciler) handleAPIError(ctx context.Context, pipeline *Pipeline, err error) (ctrl.Result, error) {
    if isRateLimitError(err) {
        return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
    }

    if isValidationError(err) {
        // Update condition and don't retry immediately
        meta.SetStatusCondition(&pipeline.Status.Conditions, metav1.Condition{
            Type:    "Synced",
            Status:  metav1.ConditionFalse,
            Reason:  "ValidationFailed",
            Message: err.Error(),
        })
        r.Status().Update(ctx, pipeline)
        return ctrl.Result{}, nil // Don't requeue
    }

    // Other errors: return for exponential backoff
    return ctrl.Result{}, fmt.Errorf("fleet API error: %w", err)
}
```

## Testing Best Practices

**Use envtest for integration tests:**
```go
import (
    "sigs.k8s.io/controller-runtime/pkg/envtest"
)

var testEnv *envtest.Environment

func TestMain(m *testing.M) {
    testEnv = &envtest.Environment{
        CRDDirectoryPaths: []string{filepath.Join("..", "config", "crd", "bases")},
    }

    cfg, err := testEnv.Start()
    // ... setup test clients

    code := m.Run()
    testEnv.Stop()
    os.Exit(code)
}
```

**Mock Fleet Management API:**
```go
type mockFleetClient struct {
    pipelines map[string]*Pipeline
    callCount int
}

func (m *mockFleetClient) UpsertPipeline(ctx context.Context, req *UpsertRequest) (*Pipeline, error) {
    m.callCount++
    m.pipelines[req.Pipeline.Name] = req.Pipeline
    return req.Pipeline, nil
}

func TestReconcile_SkipsWhenNoChange(t *testing.T) {
    mock := &mockFleetClient{}
    reconciler := &PipelineReconciler{fleetClient: mock}

    pipeline := &Pipeline{
        ObjectMeta: metav1.ObjectMeta{Generation: 1},
        Status:     PipelineStatus{ObservedGeneration: 1},
    }

    reconciler.Reconcile(ctx, req)

    // Should not call API when generation matches
    assert.Equal(t, 0, mock.callCount)
}
```

## Monitoring and Observability

**Essential metrics:**

```go
import (
    "github.com/prometheus/client_golang/prometheus"
    "sigs.k8s.io/controller-runtime/pkg/metrics"
)

var (
    fleetAPICallsTotal = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "fleet_api_calls_total",
            Help: "Total Fleet Management API calls",
        },
        []string{"operation", "status"},
    )

    reconcileErrorsTotal = prometheus.NewCounter(
        prometheus.CounterOpts{
            Name: "pipeline_reconcile_errors_total",
            Help: "Total reconciliation errors",
        },
    )
)

func init() {
    metrics.Registry.MustRegister(fleetAPICallsTotal, reconcileErrorsTotal)
}

// In code:
fleetAPICallsTotal.WithLabelValues("upsert", "success").Inc()
```

**Watch these controller-runtime metrics:**
- `workqueue_depth{name="pipeline"}` - Growing = reconcile too slow
- `workqueue_work_duration_seconds{name="pipeline"}` - Reconcile performance
- `controller_runtime_reconcile_total{result="error"}` - Error rate
- `controller_runtime_reconcile_total{result="requeue"}` - Requeue rate

## Pre-Shipping Checklist

Before shipping this controller, verify:

- [ ] ObservedGeneration pattern implemented to skip unnecessary reconciles
- [ ] Status updates use Status().Update() not Update()
- [ ] Finalizer logic handles 404 errors gracefully
- [ ] No long-running operations block Reconcile()
- [ ] Errors are returned (not swallowed with Requeue: true)
- [ ] Fleet Management API calls only happen when spec changes
- [ ] Rate limiting implemented in API client (3 req/s)
- [ ] Status conditions follow Kubernetes conventions
- [ ] Metrics and logging for observability
- [ ] Integration tests with envtest
- [ ] Unit tests with mocked Fleet Management API

# Go Best Practices

## Error Handling

**Return errors, don't panic:**
```go
// CORRECT
func (r *PipelineReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    pipeline := &Pipeline{}
    if err := r.Get(ctx, req.NamespacedName, pipeline); err != nil {
        return ctrl.Result{}, client.IgnoreNotFound(err)
    }

    apiResp, err := r.fleetClient.UpsertPipeline(ctx, buildRequest(pipeline))
    if err != nil {
        return ctrl.Result{}, fmt.Errorf("failed to upsert pipeline: %w", err)
    }

    return ctrl.Result{}, nil
}
```

**Wrap errors with context:**
```go
// Use %w for error wrapping
if err := r.fleetClient.UpsertPipeline(ctx, req); err != nil {
    return fmt.Errorf("upsert pipeline %s/%s: %w", pipeline.Namespace, pipeline.Name, err)
}

// Caller can use errors.Is() or errors.As()
if errors.Is(err, ErrRateLimit) {
    return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
}
```

**Custom error types:**
```go
type FleetAPIError struct {
    StatusCode int
    Operation  string
    Message    string
}

func (e *FleetAPIError) Error() string {
    return fmt.Sprintf("%s failed (HTTP %d): %s", e.Operation, e.StatusCode, e.Message)
}

// In controller:
if apiErr, ok := err.(*FleetAPIError); ok {
    if apiErr.StatusCode == 400 {
        meta.SetStatusCondition(&pipeline.Status.Conditions, metav1.Condition{
            Type:    "Synced",
            Status:  metav1.ConditionFalse,
            Reason:  "ValidationFailed",
            Message: apiErr.Message,
        })
        return ctrl.Result{}, nil
    }
}
```

## Defer for Resource Cleanup

**Always defer Close():**
```go
func (c *FleetClient) UpsertPipeline(ctx context.Context, req *UpsertPipelineRequest) (*Pipeline, error) {
    body, err := json.Marshal(req)
    if err != nil {
        return nil, err
    }

    httpReq, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"UpsertPipeline", bytes.NewReader(body))
    if err != nil {
        return nil, err
    }

    resp, err := c.httpClient.Do(httpReq)
    if err != nil {
        return nil, err
    }
    defer resp.Body.Close() // ALWAYS defer Close()

    if resp.StatusCode != http.StatusOK {
        body, _ := io.ReadAll(resp.Body)
        return nil, &FleetAPIError{
            StatusCode: resp.StatusCode,
            Operation:  "UpsertPipeline",
            Message:    string(body),
        }
    }

    var pipeline Pipeline
    if err := json.NewDecoder(resp.Body).Decode(&pipeline); err != nil {
        return nil, err
    }

    return &pipeline, nil
}
```

## Interfaces and Composition

**Define interfaces in consumer package:**
```go
// In internal/controller/pipeline_controller.go
type FleetPipelineClient interface {
    UpsertPipeline(ctx context.Context, req *UpsertPipelineRequest) (*Pipeline, error)
    DeletePipeline(ctx context.Context, id string) error
    GetPipeline(ctx context.Context, id string) (*Pipeline, error)
}

type PipelineReconciler struct {
    client.Client
    FleetClient FleetPipelineClient // Interface, not concrete type
    Scheme      *runtime.Scheme
}

// Easy to mock for testing
type mockFleetClient struct {
    pipelines map[string]*Pipeline
}

func (m *mockFleetClient) UpsertPipeline(ctx context.Context, req *UpsertPipelineRequest) (*Pipeline, error) {
    m.pipelines[req.Pipeline.Name] = req.Pipeline
    return req.Pipeline, nil
}
```

**Verify interface implementation at compile time:**
```go
// At package level
var _ reconcile.Reconciler = &PipelineReconciler{}
var _ FleetPipelineClient = &FleetClient{}
```

## Structured Logging

**Use controller-runtime logging:**
```go
import "sigs.k8s.io/controller-runtime/pkg/log"

func (r *PipelineReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    log := log.FromContext(ctx)

    // Structured logging with key-value pairs
    log.Info("reconciling pipeline",
        "namespace", req.Namespace,
        "name", req.Name)

    // Error logging
    if err != nil {
        log.Error(err, "failed to upsert pipeline",
            "pipelineID", pipeline.Status.ID,
            "generation", pipeline.Generation)
    }
}
```

## Range Loop Pointer Gotcha

**Be careful with pointers in range loops:**
```go
// WRONG: All pointers reference same variable
var pipelines []*Pipeline
for _, p := range pipelineSlice {
    pipelines = append(pipelines, &p) // BUG!
}

// CORRECT: Take address of slice element
for i := range pipelineSlice {
    pipelines = append(pipelines, &pipelineSlice[i])
}

// Or copy value
for _, p := range pipelineSlice {
    pCopy := p
    pipelines = append(pipelines, &pCopy)
}
```

## Table-Driven Tests

```go
func TestBuildUpsertRequest(t *testing.T) {
    tests := []struct {
        name     string
        pipeline *Pipeline
        want     *UpsertPipelineRequest
        wantErr  bool
    }{
        {
            name: "basic pipeline",
            pipeline: &Pipeline{
                Spec: PipelineSpec{
                    Name:     "test",
                    Contents: "config",
                    Enabled:  true,
                },
            },
            want: &UpsertPipelineRequest{
                Pipeline: &FleetPipeline{
                    Name:     "test",
                    Contents: "config",
                    Enabled:  true,
                },
            },
            wantErr: false,
        },
        {
            name: "empty contents",
            pipeline: &Pipeline{
                Spec: PipelineSpec{Name: "test"},
            },
            wantErr: true,
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            got, err := buildUpsertRequest(tt.pipeline)
            if (err != nil) != tt.wantErr {
                t.Errorf("error = %v, wantErr %v", err, tt.wantErr)
                return
            }
            if !reflect.DeepEqual(got, tt.want) {
                t.Errorf("got %v, want %v", got, tt.want)
            }
        })
    }
}
```

## Context Usage

**Always pass context:**
```go
func (c *FleetClient) UpsertPipeline(ctx context.Context, req *UpsertPipelineRequest) (*Pipeline, error) {
    httpReq, err := http.NewRequestWithContext(ctx, "POST", c.url, body)
    if err != nil {
        return nil, err
    }

    // Respects context cancellation/timeout
    resp, err := c.httpClient.Do(httpReq)
    if err != nil {
        return nil, err
    }
    defer resp.Body.Close()

    // ...
}
```

## Critical Patterns Summary

**Must follow:**
1. Return errors, don't panic
2. Use defer for cleanup (Close, Unlock)
3. Define interfaces in consumer package
4. Use pointers for Kubernetes objects
5. Check map presence with comma-ok
6. Use context for cancellation
7. Table-driven tests
8. Structured logging
9. Error wrapping with %w
10. Verify interface implementation at compile time

**Must avoid:**
1. Starting goroutines in Reconcile
2. Mutating loop variables in range
3. Ignoring errors
4. Using fmt.Sprintf for errors
5. Not deferring Close()
6. Type assertions without ok check
7. String concatenation for errors
