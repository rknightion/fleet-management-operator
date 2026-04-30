# API Reference

## Packages
- [fleetmanagement.grafana.com/v1alpha1](#fleetmanagementgrafanacomv1alpha1)


## fleetmanagement.grafana.com/v1alpha1

Package v1alpha1 contains API Schema definitions for the fleetmanagement v1alpha1 API group.

### Resource Types
- [Collector](#collector)
- [CollectorDiscovery](#collectordiscovery)
- [ExternalAttributeSync](#externalattributesync)
- [Pipeline](#pipeline)
- [PipelineDiscovery](#pipelinediscovery)
- [RemoteAttributePolicy](#remoteattributepolicy)
- [TenantPolicy](#tenantpolicy)



#### AttributeMapping



AttributeMapping describes how to project a source record into a
(collectorID, attributes) tuple.



_Appears in:_
- [ExternalAttributeSyncSpec](#externalattributesyncspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `collectorIDField` _string_ | CollectorIDField is the source field whose value identifies the<br />target collector. |  | MinLength: 1 <br /> |
| `attributeFields` _object (keys:string, values:string)_ | AttributeFields maps an output attribute key to the source field<br />whose value becomes its value. Keys with the reserved "collector."<br />prefix are rejected by the API server (CEL) and the validating<br />webhook. |  | MaxProperties: 100 <br />MinProperties: 1 <br /> |
| `requiredKeys` _string array_ | RequiredKeys is the set of source fields that must be present for a<br />record to be applied. A record missing any required key is skipped<br />(counted in RecordsSeen but not RecordsApplied). |  | items:MinLength: 1 <br />Optional: \{\} <br /> |


#### AttributeOwnerKind

_Underlying type:_ _string_

AttributeOwnerKind identifies which CR owns a remote-attribute key on a
collector. Phase 1 only writes `Collector`; later phases add the others
without breaking the schema.

_Validation:_
- Enum: [Collector RemoteAttributePolicy ExternalAttributeSync]

_Appears in:_
- [AttributeOwnership](#attributeownership)

| Field | Description |
| --- | --- |
| `Collector` |  |
| `RemoteAttributePolicy` |  |
| `ExternalAttributeSync` |  |


#### AttributeOwnership



AttributeOwnership records the owner and current value of one remote
attribute key.



_Appears in:_
- [CollectorStatus](#collectorstatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `key` _string_ | Key is the remote-attribute key. |  |  |
| `ownerKind` _[AttributeOwnerKind](#attributeownerkind)_ | OwnerKind identifies which kind of CR owns this key. |  | Enum: [Collector RemoteAttributePolicy ExternalAttributeSync] <br /> |
| `ownerName` _string_ | OwnerName is the namespaced name of the owning CR (in the form<br />"namespace/name"). |  |  |
| `value` _string_ | Value is the value last written for this key. |  |  |


#### Collector



Collector is the Schema for the collectors API.





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `fleetmanagement.grafana.com/v1alpha1` | | |
| `kind` _string_ | `Collector` | | |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.31/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  | Optional: \{\} <br /> |
| `spec` _[CollectorSpec](#collectorspec)_ | spec defines the desired state of the Collector. |  | Required: \{\} <br /> |
| `status` _[CollectorStatus](#collectorstatus)_ | status defines the observed state of the Collector. |  | Optional: \{\} <br /> |


#### CollectorDiscovery



CollectorDiscovery configures a periodic mirror of Fleet Management
collectors into the cluster as Collector CRs. The Collector reconciler
then manages remote attributes on each mirrored CR; this resource only
owns the CR's existence (creation when a collector appears in Fleet,
deletion or stale-marking when it disappears).





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `fleetmanagement.grafana.com/v1alpha1` | | |
| `kind` _string_ | `CollectorDiscovery` | | |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.31/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  | Optional: \{\} <br /> |
| `spec` _[CollectorDiscoverySpec](#collectordiscoveryspec)_ | spec defines the desired state. |  | Required: \{\} <br /> |
| `status` _[CollectorDiscoveryStatus](#collectordiscoverystatus)_ | status defines the observed state. |  | Optional: \{\} <br /> |


#### CollectorDiscoverySpec



CollectorDiscoverySpec configures a periodic poll-and-mirror cycle
against Fleet Management's ListCollectors. Each Fleet collector that
matches the selector becomes a Collector CR in the target namespace.



_Appears in:_
- [CollectorDiscovery](#collectordiscovery)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `pollInterval` _string_ | PollInterval is how often the controller calls Fleet's<br />ListCollectors. Webhook-enforced minimum is 1 minute to protect<br />the shared 3 req/s rate limiter. | 5m | Optional: \{\} <br /> |
| `selector` _[PolicySelector](#policyselector)_ | Selector is the server-side filter passed to ListCollectors.<br />Reuses the PolicySelector shape: matchers AND'd, OR'd with<br />explicit collectorIDs. An empty selector means "match every<br />collector" (server-wide ListCollectors call) — accepted but<br />expensive on large fleets. |  | Optional: \{\} <br /> |
| `targetNamespace` _string_ | TargetNamespace is the namespace where mirrored Collector CRs are<br />created. Defaults to this CollectorDiscovery's own namespace. |  | Optional: \{\} <br /> |
| `includeInactive` _boolean_ | IncludeInactive mirrors Fleet records with markedInactiveAt set.<br />Default false skips them — the typical case is "show me only<br />collectors that are currently expected to ping in". | false | Optional: \{\} <br /> |
| `policy` _[DiscoveryPolicy](#discoverypolicy)_ | Policy controls how the controller reacts to Fleet-side changes. |  | Optional: \{\} <br /> |


#### CollectorDiscoveryStatus



CollectorDiscoveryStatus reports the most recent poll outcome.



_Appears in:_
- [CollectorDiscovery](#collectordiscovery)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `observedGeneration` _integer_ | ObservedGeneration reflects the most recently observed spec. |  | Optional: \{\} <br /> |
| `lastSyncTime` _[Time](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.31/#time-v1-meta)_ | LastSyncTime is the timestamp of the most recent ListCollectors<br />call (success or failure). |  | Optional: \{\} <br /> |
| `lastSuccessTime` _[Time](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.31/#time-v1-meta)_ | LastSuccessTime is the timestamp of the most recent<br />ListCollectors call that produced a status update without error. |  | Optional: \{\} <br /> |
| `collectorsObserved` _integer_ | CollectorsObserved is the count of collectors returned by the<br />last ListCollectors call (after include-inactive filtering). |  | Optional: \{\} <br /> |
| `collectorsManaged` _integer_ | CollectorsManaged is the count of Collector CRs in the target<br />namespace currently labeled as managed by this discovery. |  | Optional: \{\} <br /> |
| `staleCollectors` _string array_ | StaleCollectors lists collector IDs whose CR still exists but no<br />longer appears in ListCollectors. Only populated when<br />policy.onCollectorRemoved=Keep. |  | Optional: \{\} <br /> |
| `conflicts` _[DiscoveryConflict](#discoveryconflict) array_ | Conflicts records the most recent cases (up to 100) where the<br />controller could not create or claim a CR due to a name/ownership<br />conflict. When the cap is hit, a TruncatedConflicts condition is<br />set; check events for the full conflict list. |  | MaxItems: 100 <br />Optional: \{\} <br /> |
| `conditions` _[Condition](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.31/#condition-v1-meta) array_ | Conditions represent the current state of the CollectorDiscovery.<br />See docs/conditions.md for the cross-CRD condition registry. |  | Optional: \{\} <br /> |


#### CollectorSpec



CollectorSpec defines the desired state of a Fleet Management collector.

Note: collectors register themselves with Fleet Management via
RegisterCollector — this CR does not create them. spec.id binds the CR to
an already-registered collector; if that collector has not yet registered,
reconcile will keep retrying and surface the situation in status.



_Appears in:_
- [Collector](#collector)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `id` _string_ | ID is the Fleet Management collector ID. Required and immutable after<br />creation. Immutability is declared via a CEL rule so the API server<br />enforces it independently of the validating webhook (defence-in-depth<br />and discoverable to schema consumers). |  | MinLength: 1 <br /> |
| `name` _string_ | Name is the optional display name set on the collector in Fleet<br />Management. If empty, the existing server-side name is preserved. |  | Optional: \{\} <br /> |
| `enabled` _boolean_ | Enabled toggles the collector in Fleet Management. nil leaves the<br />existing server-side value untouched (so that the operator does not<br />fight a value set elsewhere unless the user explicitly wants to). |  | Optional: \{\} <br /> |
| `remoteAttributes` _object (keys:string, values:string)_ | RemoteAttributes managed by this CR. Keys with prefix "collector." are<br />reserved by Fleet Management and rejected by the API server (CEL) and<br />the validating webhook. Each value is capped at 1024 characters by<br />the admission webhook — values are user-facing strings, not<br />configuration blobs, so the cap protects etcd. Removing a key from<br />this map removes it from Fleet (delete-detected via<br />status.attributeOwners). |  | MaxProperties: 100 <br />Optional: \{\} <br /> |


#### CollectorStatus



CollectorStatus reflects observed state from Fleet Management plus the
operator's bookkeeping for delete-detection.



_Appears in:_
- [Collector](#collector)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `observedGeneration` _integer_ | ObservedGeneration reflects the generation of the most recently<br />observed Collector spec. |  | Optional: \{\} <br /> |
| `registered` _boolean_ | Registered is true if the collector has been observed in Fleet<br />Management (i.e. it has called RegisterCollector at least once). |  | Optional: \{\} <br /> |
| `lastPing` _[Time](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.31/#time-v1-meta)_ | LastPing is the most recent ping timestamp as reported by Fleet<br />Management. May lag relative to actual collector activity. |  | Optional: \{\} <br /> |
| `name` _string_ | Name is the display name observed from Fleet Management. |  | Optional: \{\} <br /> |
| `enabled` _boolean_ | Enabled is the remote configuration enabled state observed from Fleet<br />Management. nil means Fleet did not return the field. |  | Optional: \{\} <br /> |
| `collectorType` _[CollectorType](#collectortype)_ | CollectorType is the type the collector reported on registration. |  | Enum: [Alloy OpenTelemetryCollector Unspecified] <br />Optional: \{\} <br /> |
| `createdAt` _[Time](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.31/#time-v1-meta)_ | CreatedAt is the timestamp when the collector was created in Fleet<br />Management. |  | Optional: \{\} <br /> |
| `updatedAt` _[Time](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.31/#time-v1-meta)_ | UpdatedAt is the timestamp when the collector was last updated in Fleet<br />Management. |  | Optional: \{\} <br /> |
| `markedInactiveAt` _[Time](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.31/#time-v1-meta)_ | MarkedInactiveAt is the timestamp when Fleet Management marked the<br />collector inactive. |  | Optional: \{\} <br /> |
| `localAttributes` _object (keys:string, values:string)_ | LocalAttributes are the attributes the collector reports about itself<br />(e.g. collector.os=linux). Read-only — set by the collector, not the<br />operator. |  | Optional: \{\} <br /> |
| `effectiveRemoteAttributes` _object (keys:string, values:string)_ | EffectiveRemoteAttributes is the merged set of remote attributes last<br />successfully written to Fleet Management for this collector. In Phase<br />1 this is exactly spec.remoteAttributes; later phases add policy and<br />external-sync layers. |  | Optional: \{\} <br /> |
| `attributeOwners` _[AttributeOwnership](#attributeownership) array_ | AttributeOwners records which CR owns each remote-attribute key. Used<br />by the controller to detect and remove keys when their owner stops<br />claiming them. |  | Optional: \{\} <br /> |
| `conditions` _[Condition](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.31/#condition-v1-meta) array_ | Conditions represent the current state of the Collector resource.<br />Standard condition types:<br />- "Ready": Collector successfully reconciled (attributes synced, status mirrored).<br />- "Synced": Last reconciliation succeeded. |  | Optional: \{\} <br /> |


#### CollectorType

_Underlying type:_ _string_

CollectorType mirrors the Fleet Management collector type enum and is set
by the controller from observed state — it is read-only on the spec.

_Validation:_
- Enum: [Alloy OpenTelemetryCollector Unspecified]

_Appears in:_
- [CollectorStatus](#collectorstatus)

| Field | Description |
| --- | --- |
| `Alloy` |  |
| `OpenTelemetryCollector` |  |
| `Unspecified` |  |


#### ConfigType

_Underlying type:_ _string_

ConfigType represents the type of collector configuration

_Validation:_
- Enum: [Alloy OpenTelemetryCollector]

_Appears in:_
- [PipelineDiscoverySelector](#pipelinediscoveryselector)
- [PipelineSpec](#pipelinespec)

| Field | Description |
| --- | --- |
| `Alloy` | ConfigTypeAlloy represents Grafana Alloy configuration syntax<br /> |
| `OpenTelemetryCollector` | ConfigTypeOpenTelemetryCollector represents OpenTelemetry Collector configuration syntax<br /> |


#### DiscoveryConflict



DiscoveryConflict records a single conflict between the desired CR
and an existing one with the same name.



_Appears in:_
- [CollectorDiscoveryStatus](#collectordiscoverystatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `collectorID` _string_ | CollectorID is the Fleet collector ID whose mirror CR could not<br />be created. Used as the list-map key. |  |  |
| `crName` _string_ | CRName is the metadata.name the controller computed for the CR. |  |  |
| `reason` _[DiscoveryConflictReason](#discoveryconflictreason)_ | Reason classifies the conflict. |  | Enum: [NotOwnedByDiscovery OwnedByOtherDiscovery NameSanitizationFailed] <br /> |


#### DiscoveryConflictReason

_Underlying type:_ _string_

DiscoveryConflictReason enumerates the reasons a discovered CR could
not be created or claimed.

_Validation:_
- Enum: [NotOwnedByDiscovery OwnedByOtherDiscovery NameSanitizationFailed]

_Appears in:_
- [DiscoveryConflict](#discoveryconflict)

| Field | Description |
| --- | --- |
| `NotOwnedByDiscovery` | DiscoveryConflictNotOwned indicates a Collector CR with the<br />desired name exists but is not labeled as managed by any<br />discovery — likely a manually-created CR. Skipped.<br /> |
| `OwnedByOtherDiscovery` | DiscoveryConflictOwnedByOther indicates a Collector CR with the<br />desired name exists and is labeled as managed by a different<br />CollectorDiscovery. First-write wins; the second discovery skips.<br /> |
| `NameSanitizationFailed` | DiscoveryConflictSanitizeFailed indicates the collector ID could<br />not be sanitized to a valid DNS-1123 name even with the hash<br />suffix (e.g., empty ID after sanitization).<br /> |


#### DiscoveryOnConflictAction

_Underlying type:_ _string_

DiscoveryOnConflictAction selects what the controller does when a
Collector CR with the desired name already exists and is not labeled
as managed by this discovery. v1 only ships Skip; TakeOwnership is
reserved for v2 once a clear opt-in path is designed.

_Validation:_
- Enum: [Skip]

_Appears in:_
- [DiscoveryPolicy](#discoverypolicy)

| Field | Description |
| --- | --- |
| `Skip` |  |


#### DiscoveryOnRemovedAction

_Underlying type:_ _string_

DiscoveryOnRemovedAction selects what the CollectorDiscovery controller
does when a previously-discovered collector no longer appears in
ListCollectors. Keep (default) leaves the CR in place with a stale
annotation; Delete removes it (the existing Collector finalizer then
issues REMOVE ops to Fleet, which 404s for a vanished collector — net
no-op).

_Validation:_
- Enum: [Keep Delete]

_Appears in:_
- [DiscoveryPolicy](#discoverypolicy)

| Field | Description |
| --- | --- |
| `Keep` |  |
| `Delete` |  |


#### DiscoveryPolicy



DiscoveryPolicy bundles lifecycle decisions the controller respects.



_Appears in:_
- [CollectorDiscoverySpec](#collectordiscoveryspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `onCollectorRemoved` _[DiscoveryOnRemovedAction](#discoveryonremovedaction)_ | OnCollectorRemoved chooses the controller's response when a<br />previously-discovered collector no longer appears in<br />ListCollectors. | Keep | Enum: [Keep Delete] <br />Optional: \{\} <br /> |
| `onConflict` _[DiscoveryOnConflictAction](#discoveryonconflictaction)_ | OnConflict chooses the controller's response when a Collector CR<br />with the desired name already exists and is not labeled as<br />managed by this discovery. v1 only ships Skip. | Skip | Enum: [Skip] <br />Optional: \{\} <br /> |


#### ExternalAttributeSync



ExternalAttributeSync pulls attributes from an external system on a
schedule and reflects them onto matched collectors as remote attributes.





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `fleetmanagement.grafana.com/v1alpha1` | | |
| `kind` _string_ | `ExternalAttributeSync` | | |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.31/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  | Optional: \{\} <br /> |
| `spec` _[ExternalAttributeSyncSpec](#externalattributesyncspec)_ | spec defines the desired state. |  | Required: \{\} <br /> |
| `status` _[ExternalAttributeSyncStatus](#externalattributesyncstatus)_ | status defines the observed state. |  | Optional: \{\} <br /> |


#### ExternalAttributeSyncSpec



ExternalAttributeSyncSpec defines a scheduled external-source pull whose
output becomes remote attributes on selected collectors.



_Appears in:_
- [ExternalAttributeSync](#externalattributesync)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `source` _[ExternalSource](#externalsource)_ | Source identifies the kind and configuration of the external system. |  |  |
| `schedule` _string_ | Schedule is either a Go duration ("5m", "30s") or a cron expression<br />("*/15 * * * *"). Required. |  | MinLength: 1 <br /> |
| `selector` _[PolicySelector](#policyselector)_ | Selector picks the collectors this sync targets. Reuses the<br />PolicySelector shape: matchers AND'd, OR'd with explicit collectorIDs. |  |  |
| `mapping` _[AttributeMapping](#attributemapping)_ | Mapping projects source records into collector attributes. |  |  |
| `allowEmptyResults` _boolean_ | AllowEmptyResults gates the empty-result safety guard. When false<br />(default), a Fetch that returns zero records after a previous run<br />returned at least one is treated as a probable misconfiguration —<br />the previous owned-keys claim is preserved and a Stalled condition<br />is set. | false | Optional: \{\} <br /> |


#### ExternalAttributeSyncStatus



ExternalAttributeSyncStatus reflects the controller's view of the most
recent fetch.



_Appears in:_
- [ExternalAttributeSync](#externalattributesync)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `observedGeneration` _integer_ | ObservedGeneration reflects the most recently observed spec. |  | Optional: \{\} <br /> |
| `lastSyncTime` _[Time](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.31/#time-v1-meta)_ | LastSyncTime is the timestamp of the most recent Fetch attempt. |  | Optional: \{\} <br /> |
| `lastSuccessTime` _[Time](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.31/#time-v1-meta)_ | LastSuccessTime is the timestamp of the most recent Fetch that<br />produced a status update. May trail LastSyncTime if the most recent<br />fetch was suppressed by the empty-result guard or failed. |  | Optional: \{\} <br /> |
| `recordsSeen` _integer_ | RecordsSeen is the count of records returned by the last fetch. |  | Optional: \{\} <br /> |
| `recordsApplied` _integer_ | RecordsApplied is the count of records that produced an attribute<br />update (i.e., passed RequiredKeys and selector). |  | Optional: \{\} <br /> |
| `ownedKeys` _[OwnedKeyEntry](#ownedkeyentry) array_ | OwnedKeys is the canonical claim list as of the last successful<br />fetch, capped at 1000 entries. The Collector controller reads this<br />when computing merged desired state. When the cap is hit, a Truncated<br />condition is set — attributes for collectors beyond the cap may not<br />be removed on CR deletion; shard sources with >1000 collectors. |  | MaxItems: 1000 <br />Optional: \{\} <br /> |
| `conditions` _[Condition](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.31/#condition-v1-meta) array_ | Conditions represent the current state of the ExternalAttributeSync.<br />See docs/conditions.md for the cross-CRD condition registry. |  | Optional: \{\} <br /> |


#### ExternalSource



ExternalSource is the union-typed source configuration referenced by an
ExternalAttributeSync. Exactly one of HTTP / SQL must be populated and
must match Kind.



_Appears in:_
- [ExternalAttributeSyncSpec](#externalattributesyncspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `kind` _[ExternalSourceKind](#externalsourcekind)_ |  |  | Enum: [HTTP SQL] <br /> |
| `http` _[HTTPSourceSpec](#httpsourcespec)_ |  |  |  |
| `sql` _[SQLSourceSpec](#sqlsourcespec)_ |  |  |  |
| `secretRef` _[SecretReference](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.31/#secretreference-v1-core)_ |  |  |  |


#### ExternalSourceKind

_Underlying type:_ _string_

ExternalSourceKind enumerates the supported external attribute source
kinds. Phase 3 ships HTTP; SQL arrives in Phase 4.

_Validation:_
- Enum: [HTTP SQL]

_Appears in:_
- [ExternalSource](#externalsource)

| Field | Description |
| --- | --- |
| `HTTP` |  |
| `SQL` |  |


#### HTTPSourceSpec



HTTPSourceSpec configures an HTTP/JSON external source.



_Appears in:_
- [ExternalSource](#externalsource)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `url` _string_ | URL is the fully-qualified endpoint to fetch records from. |  | MinLength: 1 <br /> |
| `method` _string_ | Method is the HTTP verb to use. Defaults to GET. | GET | Enum: [GET POST] <br />Optional: \{\} <br /> |
| `recordsPath` _string_ | RecordsPath is a dotted path into the response JSON identifying the<br />array of records. Empty means the response root is the array itself.<br />Examples: "data", "result.items". |  | Optional: \{\} <br /> |




#### OwnedKeyEntry



OwnedKeyEntry records the keys and values this ExternalAttributeSync
claims for a specific collector. The Collector controller reads these
directly when computing the merged desired state — values flow from this
status field (set on each successful Fetch) into Fleet without re-running
the source.



_Appears in:_
- [ExternalAttributeSyncStatus](#externalattributesyncstatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `collectorID` _string_ |  |  |  |
| `attributes` _object (keys:string, values:string)_ | Attributes maps the attribute key to the value this sync wants on<br />the named collector. Removing a key from this map drops that<br />claim — the Collector controller's diff produces a REMOVE op on<br />the next reconcile. |  | Optional: \{\} <br /> |


#### Pipeline



Pipeline is the Schema for the pipelines API





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `fleetmanagement.grafana.com/v1alpha1` | | |
| `kind` _string_ | `Pipeline` | | |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.31/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  | Optional: \{\} <br /> |
| `spec` _[PipelineSpec](#pipelinespec)_ | spec defines the desired state of Pipeline |  | Required: \{\} <br /> |
| `status` _[PipelineStatus](#pipelinestatus)_ | status defines the observed state of Pipeline |  | Optional: \{\} <br /> |


#### PipelineDiscovery



PipelineDiscovery configures a periodic import of Fleet Management pipelines
into the cluster as Pipeline CRs.





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `fleetmanagement.grafana.com/v1alpha1` | | |
| `kind` _string_ | `PipelineDiscovery` | | |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.31/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  | Optional: \{\} <br /> |
| `spec` _[PipelineDiscoverySpec](#pipelinediscoveryspec)_ | spec defines the desired state. |  | Required: \{\} <br /> |
| `status` _[PipelineDiscoveryStatus](#pipelinediscoverystatus)_ | status defines the observed state. |  | Optional: \{\} <br /> |


#### PipelineDiscoveryConflict



PipelineDiscoveryConflict records a single conflict between the desired CR
and an existing one with the same name.



_Appears in:_
- [PipelineDiscoveryStatus](#pipelinediscoverystatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `pipelineID` _string_ | PipelineID is the Fleet pipeline ID that could not be mirrored. List-map key. |  |  |
| `crName` _string_ | CRName is the metadata.name the controller computed. |  |  |
| `reason` _[PipelineDiscoveryConflictReason](#pipelinediscoveryconflictreason)_ | Reason classifies the conflict. |  | Enum: [NotOwnedByDiscovery OwnedByOtherDiscovery NameSanitizationFailed] <br /> |


#### PipelineDiscoveryConflictReason

_Underlying type:_ _string_

PipelineDiscoveryConflictReason classifies why a Pipeline CR could not be
created or claimed.

_Validation:_
- Enum: [NotOwnedByDiscovery OwnedByOtherDiscovery NameSanitizationFailed]

_Appears in:_
- [PipelineDiscoveryConflict](#pipelinediscoveryconflict)

| Field | Description |
| --- | --- |
| `NotOwnedByDiscovery` | PipelineDiscoveryConflictNotOwned indicates a Pipeline CR with the desired<br />name exists but is not labeled as managed by any discovery — likely a<br />manually-created CR. Skipped.<br /> |
| `OwnedByOtherDiscovery` | PipelineDiscoveryConflictOwnedByOther indicates a Pipeline CR with the<br />desired name exists and is labeled as managed by a different<br />PipelineDiscovery. First-write wins; the second discovery skips.<br /> |
| `NameSanitizationFailed` | PipelineDiscoveryConflictSanitizeFailed indicates the pipeline ID could<br />not be sanitized to a valid DNS-1123 name even with the hash suffix<br />(e.g., empty ID after sanitization).<br /> |


#### PipelineDiscoveryImportMode

_Underlying type:_ _string_

PipelineDiscoveryImportMode controls whether discovered Pipeline CRs are
immediately reconciled to Fleet Management or held read-only.

_Validation:_
- Enum: [Adopt ReadOnly]

_Appears in:_
- [PipelineDiscoverySpec](#pipelinediscoveryspec)

| Field | Description |
| --- | --- |
| `Adopt` | PipelineDiscoveryImportModeAdopt creates Pipeline CRs that the Pipeline<br />controller reconciles to Fleet Management immediately, except for<br />Grafana-sourced pipelines which are always read-only.<br /> |
| `ReadOnly` | PipelineDiscoveryImportModeReadOnly creates Pipeline CRs annotated with<br />fleetmanagement.grafana.com/import-mode=read-only. The Pipeline<br />controller observes Fleet state without creating or updating the pipeline.<br /> |


#### PipelineDiscoveryOnRemovedAction

_Underlying type:_ _string_

PipelineDiscoveryOnRemovedAction controls the response when a discovered
pipeline no longer appears in ListPipelines.

_Validation:_
- Enum: [Keep Delete]

_Appears in:_
- [PipelineDiscoveryPolicy](#pipelinediscoverypolicy)

| Field | Description |
| --- | --- |
| `Keep` | PipelineDiscoveryOnRemovedKeep leaves the Pipeline CR in place, marking<br />it with the stale annotation. Default.<br /> |
| `Delete` | PipelineDiscoveryOnRemovedDelete removes the Pipeline CR. The Pipeline<br />finalizer issues a DeletePipeline call; 404 = success for vanished pipelines.<br /> |


#### PipelineDiscoveryPolicy



PipelineDiscoveryPolicy bundles lifecycle decisions.



_Appears in:_
- [PipelineDiscoverySpec](#pipelinediscoveryspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `onPipelineRemoved` _[PipelineDiscoveryOnRemovedAction](#pipelinediscoveryonremovedaction)_ | OnPipelineRemoved chooses the response when a previously-discovered<br />pipeline no longer appears in ListPipelines. | Keep | Enum: [Keep Delete] <br />Optional: \{\} <br /> |


#### PipelineDiscoverySelector



PipelineDiscoverySelector filters which Fleet pipelines are imported.



_Appears in:_
- [PipelineDiscoverySpec](#pipelinediscoveryspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `configType` _[ConfigType](#configtype)_ | ConfigType limits discovery to pipelines of this type. |  | Enum: [Alloy OpenTelemetryCollector] <br />Optional: \{\} <br /> |
| `enabled` _boolean_ | Enabled limits discovery to enabled or disabled pipelines.<br />Omit to discover both. |  | Optional: \{\} <br /> |


#### PipelineDiscoverySpec



PipelineDiscoverySpec configures a periodic poll-and-import cycle against
Fleet Management's ListPipelines. Each Fleet pipeline that matches the
selector becomes a Pipeline CR in the target namespace.



_Appears in:_
- [PipelineDiscovery](#pipelinediscovery)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `pollInterval` _string_ | PollInterval is how often the controller calls ListPipelines.<br />Webhook-enforced minimum is 1 minute to protect the shared rate limiter. | 5m | Optional: \{\} <br /> |
| `selector` _[PipelineDiscoverySelector](#pipelinediscoveryselector)_ | Selector filters which Fleet pipelines are imported.<br />An empty selector means "import every pipeline" (server-wide<br />ListPipelines call) — accepted but expensive on large fleets. |  | Optional: \{\} <br /> |
| `targetNamespace` _string_ | TargetNamespace is the namespace where discovered Pipeline CRs are created.<br />Defaults to this PipelineDiscovery's own namespace. |  | Optional: \{\} <br /> |
| `importMode` _[PipelineDiscoveryImportMode](#pipelinediscoveryimportmode)_ | ImportMode controls whether discovered Pipeline CRs are immediately<br />managed (Adopt) or held read-only (ReadOnly). Individual Pipeline CRs<br />can override this via the fleetmanagement.grafana.com/import-mode=adopt<br />annotation, except Grafana-sourced pipelines which remain read-only. | Adopt | Enum: [Adopt ReadOnly] <br />Optional: \{\} <br /> |
| `policy` _[PipelineDiscoveryPolicy](#pipelinediscoverypolicy)_ | Policy controls lifecycle decisions. |  | Optional: \{\} <br /> |


#### PipelineDiscoveryStatus



PipelineDiscoveryStatus reports the most recent poll outcome.



_Appears in:_
- [PipelineDiscovery](#pipelinediscovery)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `observedGeneration` _integer_ | ObservedGeneration reflects the most recently observed spec generation. |  | Optional: \{\} <br /> |
| `lastSyncTime` _[Time](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.31/#time-v1-meta)_ | LastSyncTime is the timestamp of the most recent ListPipelines call. |  | Optional: \{\} <br /> |
| `lastSuccessTime` _[Time](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.31/#time-v1-meta)_ | LastSuccessTime is the timestamp of the most recent successful poll. |  | Optional: \{\} <br /> |
| `pipelinesObserved` _integer_ | PipelinesObserved is the count returned by the last ListPipelines call. |  | Optional: \{\} <br /> |
| `pipelinesManaged` _integer_ | PipelinesManaged is the count of Pipeline CRs labeled as managed by<br />this discovery. |  | Optional: \{\} <br /> |
| `stalePipelines` _string array_ | StalePipelines lists pipeline IDs whose CR still exists but no longer<br />appears in ListPipelines. Only populated when policy.onPipelineRemoved=Keep. |  | Optional: \{\} <br /> |
| `conflicts` _[PipelineDiscoveryConflict](#pipelinediscoveryconflict) array_ | Conflicts records cases (up to 100) where a CR could not be created<br />due to a name/ownership conflict. When the cap is hit, a TruncatedConflicts<br />condition is set; check events for the full list. |  | MaxItems: 100 <br />Optional: \{\} <br /> |
| `conditions` _[Condition](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.31/#condition-v1-meta) array_ | Conditions represent the current state of the PipelineDiscovery. |  | Optional: \{\} <br /> |




#### PipelineSource



PipelineSource defines the origin source of the pipeline



_Appears in:_
- [PipelineSpec](#pipelinespec)
- [PipelineStatus](#pipelinestatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `type` _[SourceType](#sourcetype)_ | Type specifies the source type (Git, Terraform, Grafana, Kubernetes, Unspecified).<br />Kubernetes is deprecated and kept only for backwards compatibility. |  | Enum: [Git Terraform Grafana Kubernetes Unspecified] <br />Optional: \{\} <br /> |
| `namespace` _string_ | Namespace provides additional context about the source<br />For Git: repository name or URL<br />For Terraform: workspace or module name<br />For Grafana: automated workflow namespace |  | Optional: \{\} <br /> |


#### PipelineSpec



PipelineSpec defines the desired state of Pipeline



_Appears in:_
- [Pipeline](#pipeline)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `name` _string_ | Name of the pipeline (unique identifier in Fleet Management)<br />If not specified, uses metadata.name |  | Optional: \{\} <br /> |
| `contents` _string_ | Contents of the pipeline configuration (Alloy or OpenTelemetry Collector config) |  | MinLength: 1 <br />Required: \{\} <br /> |
| `matchers` _string array_ | Matchers to assign pipeline to collectors. Uses Prometheus Alertmanager<br />syntax: key=value, key!=value, key=~regex, key!~regex. A maximum of<br />100 matchers may be set per pipeline; the cap exists to bound<br />validation and matching cost across the fleet (Fleet Management<br />evaluates matchers on every collector poll). Each matcher is<br />independently capped at 200 characters by the API server (OpenAPI<br />maxLength) and double-checked by the validating webhook. |  | MaxItems: 100 <br />items:MaxLength: 200 <br />items:MinLength: 1 <br />Optional: \{\} <br /> |
| `enabled` _boolean_ | Enabled indicates whether the pipeline is enabled for collectors | true | Optional: \{\} <br /> |
| `configType` _[ConfigType](#configtype)_ | ConfigType specifies the type of configuration (Alloy or OpenTelemetryCollector) | Alloy | Enum: [Alloy OpenTelemetryCollector] <br />Optional: \{\} <br /> |
| `source` _[PipelineSource](#pipelinesource)_ | Source specifies the origin of the pipeline (Git, Terraform, Grafana, etc.)<br />Used for tracking and grouping pipelines by their source |  | Optional: \{\} <br /> |
| `paused` _boolean_ | Paused suspends operator reconciliation. When true, the Pipeline<br />controller does not create or update this resource in Fleet Management.<br />Read-only ownership for discovered pipelines is represented by the<br />fleetmanagement.grafana.com/import-mode annotation, not by this field. | false | Optional: \{\} <br /> |


#### PipelineStatus



PipelineStatus defines the observed state of Pipeline.



_Appears in:_
- [Pipeline](#pipeline)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `id` _string_ | ID is the server-assigned pipeline ID from Fleet Management |  | Optional: \{\} <br /> |
| `observedGeneration` _integer_ | ObservedGeneration reflects the generation of the most recently observed Pipeline spec |  | Optional: \{\} <br /> |
| `createdAt` _[Time](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.31/#time-v1-meta)_ | CreatedAt is the timestamp when the pipeline was created in Fleet Management |  | Optional: \{\} <br /> |
| `updatedAt` _[Time](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.31/#time-v1-meta)_ | UpdatedAt is the timestamp when the pipeline was last updated in Fleet Management |  | Optional: \{\} <br /> |
| `source` _[PipelineSource](#pipelinesource)_ | Source is the source observed from Fleet Management. |  | Optional: \{\} <br /> |
| `revisionId` _string_ | RevisionID is the current revision ID from Fleet Management |  | Optional: \{\} <br /> |
| `conditions` _[Condition](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.31/#condition-v1-meta) array_ | Conditions represent the current state of the Pipeline resource.<br />Standard condition types:<br />- "Ready": Pipeline is successfully synced to Fleet Management<br />- "Synced": Last reconciliation succeeded<br />The status of each condition is one of True, False, or Unknown. |  | Optional: \{\} <br /> |


#### PolicySelector



PolicySelector picks the Collectors a RemoteAttributePolicy applies to.

A Collector matches the selector if it satisfies all Matchers (AND-ed
together) OR its ID appears in CollectorIDs. An empty selector matches
nothing — this is intentional defensive behavior so a partially-written
Policy never accidentally targets every collector.



_Appears in:_
- [CollectorDiscoverySpec](#collectordiscoveryspec)
- [ExternalAttributeSyncSpec](#externalattributesyncspec)
- [RemoteAttributePolicySpec](#remoteattributepolicyspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `matchers` _string array_ | Matchers in Prometheus Alertmanager syntax (=, !=, =~, !~), evaluated<br />against the matched Collector's local attributes plus its ID under<br />the synthetic key "collector.id". A maximum of 100 matchers may be<br />set per selector; the cap exists to bound validation cost and keep<br />`kubectl describe` output readable. Each matcher is independently<br />capped at 200 characters by the API server (OpenAPI maxLength) and<br />double-checked by the validating webhook. |  | MaxItems: 100 <br />items:MaxLength: 200 <br />items:MinLength: 1 <br />Optional: \{\} <br /> |
| `collectorIDs` _string array_ | CollectorIDs is an explicit list of collector IDs this policy targets.<br />OR'd with Matchers — a Collector matches if its ID appears here, even<br />if the Matchers would otherwise reject it. |  | MaxItems: 1000 <br />items:MinLength: 1 <br />Optional: \{\} <br /> |


#### RemoteAttributePolicy



RemoteAttributePolicy applies a bulk set of remote attributes to every
Collector matched by its selector.





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `fleetmanagement.grafana.com/v1alpha1` | | |
| `kind` _string_ | `RemoteAttributePolicy` | | |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.31/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  | Optional: \{\} <br /> |
| `spec` _[RemoteAttributePolicySpec](#remoteattributepolicyspec)_ | spec defines the desired state of the Policy. |  | Required: \{\} <br /> |
| `status` _[RemoteAttributePolicyStatus](#remoteattributepolicystatus)_ | status defines the observed state of the Policy. |  | Optional: \{\} <br /> |


#### RemoteAttributePolicySpec



RemoteAttributePolicySpec defines a bulk attribute assignment to all
collectors matched by a selector. Within a single Collector, this layer's
values are overridden by the Collector CR's own spec.RemoteAttributes —
the Policy is a default, the Collector CR is an override.



_Appears in:_
- [RemoteAttributePolicy](#remoteattributepolicy)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `selector` _[PolicySelector](#policyselector)_ |  |  |  |
| `attributes` _object (keys:string, values:string)_ | Attributes applied to every matched collector. Reserved-prefix keys<br />("collector.") are rejected by the API server (CEL) and the<br />validating webhook. |  | MaxProperties: 100 <br />MinProperties: 1 <br /> |
| `priority` _integer_ | Priority breaks ties when multiple policies match the same collector<br />and set the same key — higher Priority wins. Equal-priority ties are<br />broken alphabetically by namespaced name to keep behavior<br />deterministic across reconciliations. | 0 | Optional: \{\} <br /> |


#### RemoteAttributePolicyStatus



RemoteAttributePolicyStatus reflects the controller's view of which
collectors this policy is currently applied to.



_Appears in:_
- [RemoteAttributePolicy](#remoteattributepolicy)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `observedGeneration` _integer_ | ObservedGeneration reflects the generation of the most recently<br />observed Policy spec. |  | Optional: \{\} <br /> |
| `matchedCollectorIDs` _string array_ | MatchedCollectorIDs is a capped sample (up to 1000) of the sorted<br />collector IDs currently matched by this policy's selector. For the<br />authoritative count, see MatchedCount. When the cap is hit, a<br />Truncated condition is set on the status. |  | MaxItems: 1000 <br />Optional: \{\} <br /> |
| `matchedCount` _integer_ | MatchedCount is the number of collectors currently matched by this<br />policy. Maintained alongside MatchedCollectorIDs to back a typed<br />printer column without requiring kubectl to coerce a string array<br />into an integer. |  | Optional: \{\} <br /> |
| `conditions` _[Condition](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.31/#condition-v1-meta) array_ | Conditions represent the current state of the Policy.<br />See docs/conditions.md for the cross-CRD condition registry. |  | Optional: \{\} <br /> |


#### SQLSourceSpec



SQLSourceSpec configures a generic SQL external source. Reserved for
Phase 4; the type is exposed now so existing CRDs remain forward-compatible.



_Appears in:_
- [ExternalSource](#externalsource)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `driver` _string_ | Driver names the database/sql driver. Phase 4 will register<br />"postgres" and "mysql". |  | Optional: \{\} <br /> |
| `query` _string_ | Query is the SQL query to execute. Must SELECT at minimum the<br />CollectorIDField and every AttributeFields source column. |  | MinLength: 1 <br />Optional: \{\} <br /> |


#### SourceType

_Underlying type:_ _string_

SourceType represents the origin source of the pipeline

_Validation:_
- Enum: [Git Terraform Grafana Kubernetes Unspecified]

_Appears in:_
- [PipelineSource](#pipelinesource)

| Field | Description |
| --- | --- |
| `Git` | SourceTypeGit indicates pipeline originated from Git repository<br /> |
| `Terraform` | SourceTypeTerraform indicates pipeline originated from Terraform<br /> |
| `Grafana` | SourceTypeGrafana indicates pipeline originated from an automated<br />Grafana Cloud workflow, such as Instrumentation Hub. Grafana-sourced<br />pipelines are read-only from this operator's perspective.<br /> |
| `Kubernetes` | SourceTypeKubernetes indicates pipeline originated from this Kubernetes<br />operator. Deprecated: Fleet Management does not expose a Kubernetes<br />source enum; this value is accepted for compatibility but is not sent to<br />Fleet by new reconciles.<br /> |
| `Unspecified` | SourceTypeUnspecified indicates pipeline source is not specified<br /> |


#### TenantPolicy



TenantPolicy declares which K8s subjects are required to scope their
Fleet Management CR matchers to a specific set of allowed matchers. It
implements the missing per-tenant authorization layer that Fleet
Management's API does not provide natively, by leveraging K8s RBAC group
membership at admission time. Cluster-scoped because tenant boundaries
are a platform-admin concern; standard K8s RBAC on this CRD itself
controls who can create or modify policies.





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `fleetmanagement.grafana.com/v1alpha1` | | |
| `kind` _string_ | `TenantPolicy` | | |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.31/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  | Optional: \{\} <br /> |
| `spec` _[TenantPolicySpec](#tenantpolicyspec)_ | spec defines the desired tenant policy. |  | Required: \{\} <br /> |
| `status` _[TenantPolicyStatus](#tenantpolicystatus)_ | status defines the observed state of the policy. |  | Optional: \{\} <br /> |


#### TenantPolicySpec



TenantPolicySpec binds K8s subjects to a set of required matchers. When
tenant-policy enforcement is enabled on the manager, validating webhooks
for Pipeline / RemoteAttributePolicy / ExternalAttributeSync resources
require that the requesting user (after subject match) include at least
one of the union of RequiredMatchers from every matching policy in their
CR's matcher set.



_Appears in:_
- [TenantPolicy](#tenantpolicy)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `subjects` _[Subject](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.31/#subject-v1-rbac) array_ | Subjects this policy applies to. A subject matches the admission<br />request when its Kind+Name (and Namespace, for ServiceAccount) line up<br />with admission.Request.UserInfo. Reuses rbacv1.Subject so cluster<br />admins can copy bindings from existing RoleBindings. |  | MinItems: 1 <br /> |
| `requiredMatchers` _string array_ | RequiredMatchers is the set of matchers (Prometheus Alertmanager<br />syntax: key=value, key!=value, key=~regex, key!~regex) that the CR's<br />matcher set must contain at least one of. Multiple matching policies<br />contribute to the union of allowed matchers — the CR satisfies the<br />check by including ANY one element of that union. A maximum of 100<br />required matchers may be set per policy; the cap exists to bound<br />admission cost across many concurrent CR writes. Each matcher is<br />independently capped at 200 characters by the API server (OpenAPI<br />maxLength) and double-checked by the validating webhook. |  | MaxItems: 100 <br />MinItems: 1 <br />items:MaxLength: 200 <br /> |
| `namespaceSelector` _[LabelSelector](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.31/#labelselector-v1-meta)_ | NamespaceSelector limits this policy to CRs in matching namespaces. If<br />nil, the policy applies in every namespace. Selectors are evaluated<br />against namespace labels via the standard metav1.LabelSelector<br />semantics. |  | Optional: \{\} <br /> |


#### TenantPolicyStatus



TenantPolicyStatus reflects the controller's view of a TenantPolicy.

Conditions written by the TenantPolicy reconciler:

  - Valid:  True when every required matcher and selector parses; False
    with reason ParseError when any required matcher or selector is
    malformed.
  - Ready:  True when Valid=True; otherwise False.



_Appears in:_
- [TenantPolicy](#tenantpolicy)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `observedGeneration` _integer_ | ObservedGeneration tracks the last spec generation reconciled. |  | Optional: \{\} <br /> |
| `boundSubjectCount` _integer_ | BoundSubjectCount is the number of subjects (groups + users +<br />service accounts) currently declared by spec.subjects. Maintained<br />alongside the array so a typed printer column does not need to<br />coerce a slice into an integer. |  | Optional: \{\} <br /> |
| `conditions` _[Condition](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.31/#condition-v1-meta) array_ | Conditions describe the policy's current state. |  | Optional: \{\} <br /> |


