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

package fleetclient

import (
	"context"
	"errors"
	"time"

	connect "connectrpc.com/connect"
	"github.com/prometheus/client_golang/prometheus"
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
)

var (
	// Buckets are tuned for a 30s HTTP client timeout: prometheus.DefBuckets
	// tops out at 10s, so any 10-30s call would land in the +Inf bucket and
	// be invisible. The 20s and 30s buckets surface near-timeout behaviour
	// that is the most actionable signal for capacity / rate-limit issues.
	fleetAPIRequestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "fleet_api_request_duration_seconds",
			Help:    "Duration of Fleet Management API requests in seconds.",
			Buckets: []float64{0.005, 0.025, 0.1, 0.5, 1, 2, 5, 10, 20, 30},
		},
		[]string{"operation", "status"},
	)
	fleetAPIRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "fleet_api_requests_total",
			Help: "Total number of Fleet Management API requests.",
		},
		[]string{"operation"},
	)
	fleetAPIErrorsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "fleet_api_errors_total",
			Help: "Total number of Fleet Management API errors.",
		},
		[]string{"operation", "status"},
	)
	// See fleetAPIRequestDuration above: matched buckets so the two
	// histograms can be compared directly. A wait that approaches 30s
	// indicates the rate limiter is starving requests right up to the
	// HTTP timeout, which presents identically to a Fleet API outage.
	fleetAPIRateLimiterWait = prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "fleet_api_rate_limiter_wait_duration_seconds",
			Help:    "Time spent waiting for the Fleet Management API rate limiter.",
			Buckets: []float64{0.005, 0.025, 0.1, 0.5, 1, 2, 5, 10, 20, 30},
		},
	)
)

func init() {
	ctrlmetrics.Registry.MustRegister(
		fleetAPIRequestDuration,
		fleetAPIRequestsTotal,
		fleetAPIErrorsTotal,
		fleetAPIRateLimiterWait,
	)
}

// metricsInterceptor records Fleet API request counts, durations, and errors.
// It must be placed AFTER the rate-limit interceptor in the chain so that
// recorded durations reflect actual API call time, not rate-limiter queue time.
func metricsInterceptor() connect.Interceptor {
	return connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			start := time.Now()
			operation := req.Spec().Procedure

			resp, err := next(ctx, req)

			duration := time.Since(start).Seconds()
			status := "ok"
			if err != nil {
				var connectErr *connect.Error
				if errors.As(err, &connectErr) {
					status = connectErr.Code().String()
				} else {
					status = "unknown"
				}
				fleetAPIErrorsTotal.WithLabelValues(operation, status).Inc()
			}
			fleetAPIRequestsTotal.WithLabelValues(operation).Inc()
			fleetAPIRequestDuration.WithLabelValues(operation, status).Observe(duration)

			return resp, err
		}
	})
}
