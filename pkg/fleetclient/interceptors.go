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
	"encoding/base64"
	"fmt"

	connect "connectrpc.com/connect"
	"golang.org/x/time/rate"
)

// rateLimitInterceptor returns a unary client interceptor that blocks on the
// limiter before each outgoing call. When the limiter is shared across multiple
// service clients (Pipeline + Collector), the 3 req/s budget is enforced as a
// single global rate, not per-service.
func rateLimitInterceptor(limiter *rate.Limiter) connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			if err := limiter.Wait(ctx); err != nil {
				return nil, fmt.Errorf("rate limiter error: %w", err)
			}
			return next(ctx, req)
		}
	}
}

// basicAuthInterceptor returns a unary client interceptor that adds an HTTP
// Basic Auth header to outgoing requests. Credentials are base64-encoded once
// at construction time and reused on every call.
func basicAuthInterceptor(username, password string) connect.UnaryInterceptorFunc {
	encoded := base64.StdEncoding.EncodeToString([]byte(username + ":" + password))
	header := "Basic " + encoded
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			req.Header().Set("Authorization", header)
			return next(ctx, req)
		}
	}
}
