/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"github.com/prometheus/client_golang/prometheus"
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
)

// Reconcile outcome label values for fleetResourceSyncedTotal. Shared across
// controllers so dashboards / alerts can rely on a stable vocabulary.
const (
	outcomeNotFound        = "NotFound"
	outcomeNoOp            = "NoOp"
	outcomeSynced          = "Synced"
	outcomeSyncFailed      = "SyncFailed"
	outcomeValidationError = "ValidationError"
	outcomeRateLimited     = "RateLimited"
	outcomeDeleted         = "Deleted"
	outcomeDeleteFailed    = "DeleteFailed"
	outcomeRecreated       = "Recreated"
	outcomeStalled         = "Stalled"
	outcomeScheduleSkipped = "ScheduleSkipped"
	outcomeTruncated       = "Truncated"
	outcomeNoMatch         = "NoMatch"
	outcomeMatched         = "Matched"
	outcomeListFailed      = "ListFailed"
	outcomeSourceFailed    = "SourceFailed"
	outcomeInvalidSchedule = "InvalidSchedule"
)

var (
	// fleetResourceSyncedTotal counts reconciliation outcomes per resource kind
	// and reason. Uses kind+reason labels only — no namespace/name to avoid
	// cardinality explosion. OBS-06.
	fleetResourceSyncedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "fleet_resource_synced_total",
			Help: "Total reconciliation outcomes by resource kind and reason.",
		},
		[]string{"kind", "reason"},
	)

	// fleetResourceSyncAge records the age (seconds since last successful sync)
	// at each reconcile, labelled by kind only to avoid per-resource cardinality
	// at 30k-Collector scale. OBS-03.
	fleetResourceSyncAge = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "fleet_resource_sync_age_seconds",
			Help:    "Age in seconds since last successful sync, sampled at each reconcile.",
			Buckets: []float64{0, 60, 300, 600, 1800, 3600, 7200},
		},
		[]string{"kind"},
	)

	// fleetExternalSyncOwnedKeys is the number of attribute keys owned by an
	// ExternalAttributeSync CR after each successful reconcile. OBS-04.
	fleetExternalSyncOwnedKeys = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "fleet_external_sync_owned_keys",
			Help: "Number of attribute keys currently owned by an ExternalAttributeSync CR.",
		},
		[]string{"namespace", "name"},
	)

	// fleetDiscoveryListSize is the number of collectors returned by the last
	// ListCollectors call for a CollectorDiscovery CR. OBS-05.
	fleetDiscoveryListSize = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "fleet_discovery_list_collectors_result_size",
			Help: "Number of collectors returned by the last ListCollectors call.",
		},
		[]string{"namespace", "name"},
	)
)

func init() {
	ctrlmetrics.Registry.MustRegister(
		fleetResourceSyncedTotal,
		fleetResourceSyncAge,
		fleetExternalSyncOwnedKeys,
		fleetDiscoveryListSize,
	)
}
