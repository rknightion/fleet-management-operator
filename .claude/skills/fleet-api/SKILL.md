---
name: fleet-api
description: Detailed Fleet Management Pipeline API documentation including endpoints, request/response formats, error handling, and examples
---

# Fleet Management Pipeline API Reference

This skill provides comprehensive documentation for the Grafana Cloud Fleet Management Pipeline API.

## Base URL and Authentication

**Base URL:** `https://fleet-management-<CLUSTER_NAME>.grafana.net/pipeline.v1.PipelineService/`

**Authentication:** Basic auth with username (stack ID) and password/token (Cloud access token)

**Rate Limits:** Management endpoints: 3 req/s (requests_per_second:api limit)

## Pipeline Object Structure

```json
{
  "name": "string (required, unique identifier)",
  "contents": "string (required, Alloy or OTEL config)",
  "matchers": ["collector.os=linux", "team!=team-a"],
  "enabled": true,
  "id": "server-assigned",
  "config_type": "CONFIG_TYPE_ALLOY | CONFIG_TYPE_OTEL",
  "source": {
    "type": "SOURCE_TYPE_GIT | SOURCE_TYPE_TERRAFORM | SOURCE_TYPE_UNSPECIFIED",
    "namespace": "string (required if type set)"
  },
  "created_at": "timestamp",
  "updated_at": "timestamp"
}
```

**Important field notes:**
- Protobuf uses snake_case; JSON responses use camelCase; both accepted in requests
- `name`: Unique identifier for the pipeline across entire Fleet Management
- `contents`: Must be properly escaped JSON string
- `matchers`: 200 character limit per matcher, Prometheus Alertmanager syntax
- `config_type`: Two values supported:
  - `CONFIG_TYPE_ALLOY`: For Grafana Alloy configuration syntax (default)
  - `CONFIG_TYPE_OTEL`: For OpenTelemetry Collector configuration syntax
- `id`: Server-assigned, use this for updates/deletes
- `source`: Optional metadata about origin (Git, Terraform, etc.)

## API Operations

### CreatePipeline

Creates a new pipeline.

**Behavior:**
- Returns 409 if name already exists
- Supports `validate_only: true` for dry-run validation

**Request:**
```json
{
  "pipeline": {
    "name": "my-pipeline",
    "contents": "prometheus.scrape \"default\" { }",
    "config_type": "CONFIG_TYPE_ALLOY",
    "matchers": ["collector.os=linux"],
    "enabled": true
  },
  "validate_only": false
}
```

### UpdatePipeline

Updates an existing pipeline by ID.

**CRITICAL:** Unset fields are removed (not preserved).

**Behavior:**
- Returns 404 if pipeline doesn't exist
- Supports `validate_only: true` for dry-run validation
- Any field not included in the request is deleted from the pipeline

**Request:**
```json
{
  "pipeline": {
    "id": "12345",
    "name": "my-pipeline",
    "contents": "updated config",
    "config_type": "CONFIG_TYPE_ALLOY",
    "matchers": ["collector.os=linux"],
    "enabled": true
  },
  "validate_only": false
}
```

**Important:** Always include all fields, even if unchanged. Missing fields will be deleted.

### UpsertPipeline (Recommended for Controllers)

Creates new or updates existing pipeline.

**Why recommended:**
- Idempotent operation
- Handles pipeline recreation gracefully
- Simpler than managing Create vs Update logic

**Behavior:**
- Creates if doesn't exist, updates if exists
- Unset fields are removed on updates
- Supports `validate_only: true` for dry-run validation

**Request:**
```json
{
  "pipeline": {
    "name": "my-pipeline",
    "contents": "config",
    "config_type": "CONFIG_TYPE_ALLOY",
    "matchers": ["env=prod"],
    "enabled": true
  },
  "validate_only": false
}
```

**Response:**
```json
{
  "pipeline": {
    "id": "server-assigned-id",
    "name": "my-pipeline",
    "contents": "config",
    "config_type": "CONFIG_TYPE_ALLOY",
    "matchers": ["env=prod"],
    "enabled": true,
    "created_at": "2024-01-15T10:30:00Z",
    "updated_at": "2024-01-15T10:30:00Z"
  }
}
```

### DeletePipeline

Deletes a pipeline by ID.

**Behavior:**
- Returns empty response on success
- Returns 404 if not found

**Request:**
```json
{
  "id": "12345"
}
```

**Important for Controllers:**
- Always handle 404 as success (already deleted)
- Used in finalizer logic

### GetPipeline

Retrieves a pipeline by ID.

**Returns:** Full pipeline object with contents

**Request:**
```json
{
  "id": "12345"
}
```

**Note:** Generally not needed in controllers - UpsertPipeline returns the full object.

### GetPipelineID

Retrieves pipeline ID by name.

**Useful for:** Looking up ID from name

**Request:**
```json
{
  "name": "my-pipeline"
}
```

**Response:**
```json
{
  "id": "12345"
}
```

### ListPipelines

Returns all pipelines matching filters.

**Filters:**
- `local_attributes`: Filter by local collector attributes
- `remote_attributes`: Filter by remote collector attributes
- `config_type`: Filter by CONFIG_TYPE_ALLOY or CONFIG_TYPE_OTEL
- `enabled`: Filter by enabled status

**Returns:** Full pipeline objects including contents

**Request:**
```json
{
  "config_type": "CONFIG_TYPE_ALLOY",
  "enabled": true
}
```

**Warning:** Expensive operation. Don't call on every reconcile due to rate limits.

### SyncPipelines

Bulk create/update/delete from common source.

**Behavior:**
- Atomic operation for GitOps workflows
- Creates pipelines not in Fleet Management
- Updates pipelines that exist with changes
- Deletes pipelines not in request but exist with same source
- All pipelines must share same `source` metadata

**Request:**
```json
{
  "pipelines": [
    {
      "name": "pipeline-1",
      "contents": "config",
      "source": {"type": "SOURCE_TYPE_GIT", "namespace": "main"}
    },
    {
      "name": "pipeline-2",
      "contents": "config",
      "source": {"type": "SOURCE_TYPE_GIT", "namespace": "main"}
    }
  ]
}
```

## Revision Tracking

### ListPipelinesRevisions

Returns all pipeline changes across all pipelines in chronological order.

**Behavior:**
- Omits pipeline contents for performance
- Shows operation: INSERT, UPDATE, DELETE

**Use case:** Audit log of all pipeline changes

### ListPipelineRevisions

Returns all revisions for a single pipeline by ID.

**Behavior:**
- Includes full pipeline snapshot with contents
- Shows operation that created each revision

**Use case:** History of changes for a specific pipeline

### GetPipelineRevision

Returns a single revision by revision_id.

**Behavior:**
- Includes full pipeline snapshot with contents

**Use case:** Retrieve specific historical version

### PipelineRevision Structure

```json
{
  "revision_id": "server-assigned",
  "snapshot": {
    "id": "12345",
    "name": "my-pipeline",
    "contents": "config at this revision",
    "matchers": ["env=prod"],
    "enabled": true,
    "config_type": "CONFIG_TYPE_ALLOY",
    "created_at": "2024-01-15T10:30:00Z",
    "updated_at": "2024-01-15T10:35:00Z"
  },
  "created_at": "2024-01-15T10:35:00Z",
  "operation": "INSERT | UPDATE | DELETE"
}
```

## Field Name Conventions

**Protobuf definitions:** snake_case (e.g., `created_at`, `config_type`)

**JSON responses:** camelCase (e.g., `createdAt`, `configType`)

**Requests:** Both formats accepted

**Go structs should use camelCase in json tags:**
```go
type Pipeline struct {
    Name      string   `json:"name"`
    Contents  string   `json:"contents"`
    Matchers  []string `json:"matchers,omitempty"`
    Enabled   bool     `json:"enabled"`
    ID        string   `json:"id,omitempty"`
    CreatedAt *time.Time `json:"createdAt,omitempty"`
    UpdatedAt *time.Time `json:"updatedAt,omitempty"`
    ConfigType string `json:"configType,omitempty"`
}
```

## Update Semantics (Critical)

**IMPORTANT:** UpdatePipeline and UpsertPipeline are NOT selective.

If you omit a field in the request, it's removed from the pipeline:

```json
// Current pipeline in Fleet Management
{
  "name": "test",
  "contents": "config",
  "enabled": true,
  "matchers": ["env=prod", "region=us-west"]
}

// Update request (missing matchers)
{
  "pipeline": {
    "name": "test",
    "contents": "config",
    "enabled": true
  }
}

// Result: matchers are REMOVED
{
  "name": "test",
  "contents": "config",
  "enabled": true,
  "matchers": []
}
```

**Controller implication:** Always include all fields from spec when calling Upsert.

## Matcher Syntax

Follows Prometheus Alertmanager syntax:

**Operators:**
- `key=value` - Equals
- `key!=value` - Not equals
- `key=~regex` - Regex match
- `key!~regex` - Regex not match

**Examples:**
- `collector.os=linux` - Matches Linux collectors
- `environment!=development` - Excludes development
- `team=~team-(a|b)` - Matches team-a or team-b
- `region!~us-.*` - Excludes US regions

**Constraints:**
- 200 character limit per matcher
- Matchers are AND'd together (all must match)
- Multiple pipelines can match same collector

**Common matcher patterns:**
- `collector.os=linux` - Operating system
- `environment=production` - Environment
- `team=platform` - Team ownership
- `region=us-west-2` - Geographic region
- `collector.type=alloy` or `collector.type=otel` - Collector type

## Configuration Content Escaping

Pipeline contents must be properly escaped for JSON.

**Using jq:**
```bash
jq --arg contents "$(cat config.alloy)" \
   --arg name "myname" \
   --argjson matchers '["collector.os=linux"]' \
   --argjson enabled true \
   '.pipeline = {name: $name, contents: $contents, matchers: $matchers, enabled: $enabled}' \
   <<< '{}'
```

**In Go:**
```go
// json.Marshal automatically escapes
pipeline := &Pipeline{
    Name:     "test",
    Contents: string(configFileBytes), // Will be escaped
    Matchers: []string{"env=prod"},
    Enabled:  true,
}
body, _ := json.Marshal(map[string]interface{}{"pipeline": pipeline})
```

## Validation

Use `validate_only: true` to test configuration without applying:

```go
req := &UpsertPipelineRequest{
    Pipeline: pipeline,
    ValidateOnly: true,
}
resp, err := client.UpsertPipeline(ctx, req)
// If err is nil, configuration is valid
// resp shows what would be created/updated
```

**Validation checks:**
- Configuration syntax (Alloy or OTEL)
- Matcher syntax
- Required fields present
- Field constraints (e.g., matcher length)

## Error Handling

### HTTP Status Codes

- **200 OK**: Success
- **400 Bad Request**: Validation error (invalid config, bad matcher syntax)
- **404 Not Found**: Pipeline doesn't exist (on get/update/delete)
- **409 Conflict**: Pipeline with name already exists (on create)
- **429 Too Many Requests**: Rate limit exceeded
- **5xx Server Error**: Transient server issues

### Error Response Format

```json
{
  "code": 3,
  "message": "Pipeline validation failed: invalid Alloy syntax",
  "details": []
}
```

### Controller Error Handling Strategy

**400 Validation Error:**
- Update status condition with error details
- Don't retry immediately (user needs to fix config)
- Set ValidationError condition

**404 Not Found:**
- On delete: treat as success (already deleted)
- On get: pipeline was deleted externally
- In finalizer: remove finalizer and continue

**409 Conflict:**
- On create: pipeline exists (shouldn't happen with Upsert)
- Rare with UpsertPipeline

**429 Rate Limit:**
- Exponential backoff
- Requeue with delay (e.g., 10 seconds)
- Implemented automatically by rate limiter

**5xx Server Error:**
- Retry with exponential backoff
- Return error from Reconcile for automatic retry

## Rate Limiting Implementation

Implement rate limiting in API client:

```go
import "golang.org/x/time/rate"

type FleetClient struct {
    baseURL    string
    httpClient *http.Client
    limiter    *rate.Limiter
    username   string
    password   string
}

func NewFleetClient(baseURL, username, password string) *FleetClient {
    return &FleetClient{
        baseURL:    baseURL,
        username:   username,
        password:   password,
        httpClient: &http.Client{Timeout: 30 * time.Second},
        limiter:    rate.NewLimiter(rate.Limit(3), 1), // 3 req/s
    }
}

func (c *FleetClient) UpsertPipeline(ctx context.Context, req *UpsertPipelineRequest) (*Pipeline, error) {
    // Wait for rate limiter before making request
    if err := c.limiter.Wait(ctx); err != nil {
        return nil, err
    }

    // Make HTTP request
    // ...
}
```

## HTTP Client Configuration

```go
httpClient := &http.Client{
    Timeout: 30 * time.Second,
    Transport: &http.Transport{
        MaxIdleConns:        100,
        IdleConnTimeout:     90 * time.Second,
        TLSHandshakeTimeout: 10 * time.Second,
    },
}
```

## Request Construction Example

```go
func (c *FleetClient) UpsertPipeline(ctx context.Context, req *UpsertPipelineRequest) (*Pipeline, error) {
    // Rate limit
    if err := c.limiter.Wait(ctx); err != nil {
        return nil, err
    }

    // Build request
    reqBody, err := json.Marshal(req)
    if err != nil {
        return nil, err
    }

    httpReq, err := http.NewRequestWithContext(
        ctx,
        "POST",
        c.baseURL+"UpsertPipeline",
        bytes.NewReader(reqBody),
    )
    if err != nil {
        return nil, err
    }

    httpReq.Header.Set("Content-Type", "application/json")
    httpReq.SetBasicAuth(c.username, c.password)

    // Execute
    resp, err := c.httpClient.Do(httpReq)
    if err != nil {
        return nil, err
    }
    defer resp.Body.Close()

    // Handle errors
    if resp.StatusCode != http.StatusOK {
        body, _ := io.ReadAll(resp.Body)
        return nil, &FleetAPIError{
            StatusCode: resp.StatusCode,
            Operation:  "UpsertPipeline",
            Message:    string(body),
        }
    }

    // Parse response
    var result struct {
        Pipeline *Pipeline `json:"pipeline"`
    }
    if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
        return nil, err
    }

    return result.Pipeline, nil
}
```

## Response Parsing and Status Mapping

```go
// Parse response (uses camelCase)
var apiPipeline Pipeline
if err := json.NewDecoder(resp.Body).Decode(&apiPipeline); err != nil {
    return nil, err
}

// Map to CRD status
pipeline.Status.ID = apiPipeline.ID
pipeline.Status.CreatedAt = (*metav1.Time)(apiPipeline.CreatedAt)
pipeline.Status.UpdatedAt = (*metav1.Time)(apiPipeline.UpdatedAt)
pipeline.Status.ObservedGeneration = pipeline.Generation

// Set conditions
meta.SetStatusCondition(&pipeline.Status.Conditions, metav1.Condition{
    Type:   "Synced",
    Status: metav1.ConditionTrue,
    Reason: "UpsertSucceeded",
})
```

## Common Issues and Solutions

### Pipeline not assigned to collectors

**Symptoms:** Pipeline created but collectors don't receive it

**Checks:**
1. Pipeline `enabled: true`?
2. Collector `enabled: true`?
3. Matcher syntax correct?
4. Collector attributes match pipeline matchers?
5. Collector polling? (default 5m poll_frequency)

**Debug:**
- Check collector logs for matcher evaluation
- Use Fleet Management UI Inventory view to see collector attributes
- Test matchers with ListCollectors API

### Configuration validation errors

**Symptoms:** Pipeline created but collectors show errors

**Causes:**
1. Invalid Alloy/OTEL configuration syntax
2. ConfigType mismatch (Alloy config with OpenTelemetryCollector type)
3. Alloy config assigned to OTEL collector or vice versa

**Solution:**
- Use `validate_only: true` flag before actual creation
- Validate configType matches contents syntax
- Validate locally with `alloy fmt` or `otelcol validate`
- Check collector internal logs for specific errors
- Use Pipeline revision history to identify breaking change

### Updates not applied

**Symptoms:** Update CRD but collectors don't see changes

**Checks:**
1. status.observedGeneration matches metadata.generation?
2. Controller logs show successful reconciliation?
3. Rate limits exceeded?
4. Collector poll interval not reached yet?

**Debug:**
- Check controller logs for API errors
- Verify status.updatedAt changed
- Check status conditions for errors

### Duplicate pipelines

**Symptoms:** Multiple pipelines with same name

**Cause:** Name collision

**Solution:**
- Pipeline name must be unique across entire Fleet Management
- Consider namespace prefixing: `{namespace}-{name}`
- Use admission webhook to prevent duplicates
- Check for external pipeline creation (Terraform, UI)


# Collector Service API Reference

The `collector.v1.CollectorService` exposes CRUD plus a JSON-patch-style bulk update for collector remote attributes. The operator uses it via the official `github.com/grafana/fleet-management-api` connect-go SDK.

## Base URL

`https://fleet-management-<CLUSTER_NAME>.grafana.net/collector.v1.CollectorService/`

Same Basic-auth credentials as the Pipeline service. The operator shares one rate limiter across both services (3 req/s combined budget for management endpoints; the higher 20 req/s budget is reserved for `RegisterCollector` and `GetConfig` which the operator does not call).

## Collector Message

```json
{
  "id": "string (required)",
  "name": "string (optional)",
  "remote_attributes": {"key": "value"},   // managed by operators / API
  "local_attributes": {"key": "value"},    // set by collector via RegisterCollector
  "enabled": true,
  "created_at": "timestamp",
  "updated_at": "timestamp",
  "marked_inactive_at": "timestamp",
  "collector_type": "COLLECTOR_TYPE_ALLOY | COLLECTOR_TYPE_OTEL"
}
```

**Important:**
- Collectors register themselves via `RegisterCollector` — operators do NOT create them.
- `collector.*` prefix is reserved for local (collector-reported) attribute keys; remote attributes must not use it.
- Max 100 remote attributes per collector.
- Remote attributes take precedence over local attributes for pipeline matcher evaluation.

## RPC Methods

| Method | Use case |
|--------|----------|
| `GetCollector(id)` | Read live collector state. The operator uses this on each Collector reconcile to mirror local attributes into status and to compute the diff. |
| `UpdateCollector(collector)` | Replace ALL fields. Unset fields are cleared. Avoid for selective updates — use BulkUpdateCollectors instead. |
| `BulkUpdateCollectors(ids, ops)` | Atomic JSON-patch-style mutation. Each op targets a path like `/remote_attributes/<key>` with op = ADD / REPLACE / REMOVE / MOVE / COPY. The operator uses this for selective attribute updates. |
| `ListCollectors(matchers)` | List collectors matching a Prometheus-style matcher set. |
| `DeleteCollector(id)`, `BulkDeleteCollectors(ids)` | Removal — the operator does not call these (collectors self-register and self-deregister). |

## BulkUpdateCollectors Operation Syntax

```
Operation {
  op: ADD | REMOVE | REPLACE | MOVE | COPY
  path: string  // RFC 6901 JSON pointer, e.g. "/remote_attributes/env"
  value: string
  oldValue: string (optional, for selective REPLACE)
  from: string (optional, for MOVE / COPY)
}
```

- Path keys with `/` or `~` must be RFC-6901 escaped (`/` -> `~1`, `~` -> `~0`). The operator handles this in `internal/controller/attributes/diff.go::remoteAttrPath`.
- The whole request is atomic per Fleet API contract: all ops succeed or none do, per-collector.
- Cross-collector partial failure IS possible when multiple ids are in one request — the operator typically calls with a single id at a time to keep error handling simple.

## Connect-go vs HTTP/JSON

The operator no longer uses hand-rolled HTTP/JSON; it uses the official connect-go generated client (`github.com/grafana/fleet-management-api/api/gen/proto/go/{pipeline,collector}/v1/...`). The connect protocol is HTTP/1.1+JSON-over-the-wire by default, so on-the-wire troubleshooting (curl, tcpdump) still works the same.

Connect error codes map back to HTTP-style status codes via `pkg/fleetclient/errors.go::connectCodeToHTTPStatus` so the controller error classification (`internal/controller/errors.go`) is unchanged: `CodeNotFound -> 404`, `CodeResourceExhausted -> 429`, `CodeInternal/Unavailable/...` -> 5xx, etc.
