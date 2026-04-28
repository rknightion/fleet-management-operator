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
)

func init() {
	ctrlmetrics.Registry.MustRegister(fleetResourceSyncedTotal)
}
