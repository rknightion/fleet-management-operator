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

var (
	// OBS-03: age histogram per kind (not per resource to avoid cardinality at 30k scale)
	fleetResourceSyncAge = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "fleet_resource_sync_age_seconds",
			Help:    "Age in seconds since last successful sync, sampled at each reconcile.",
			Buckets: []float64{0, 60, 300, 600, 1800, 3600, 7200},
		},
		[]string{"kind"},
	)

	// OBS-04: owned-key count per ExternalAttributeSync CR
	fleetExternalSyncOwnedKeys = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "fleet_external_sync_owned_keys",
			Help: "Number of attribute keys currently owned by an ExternalAttributeSync CR.",
		},
		[]string{"namespace", "name"},
	)

	// OBS-05: last ListCollectors result size per CollectorDiscovery CR
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
		fleetResourceSyncAge,
		fleetExternalSyncOwnedKeys,
		fleetDiscoveryListSize,
	)
}
