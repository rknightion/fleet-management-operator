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
	"fmt"

	connect "connectrpc.com/connect"
	collectorv1 "github.com/grafana/fleet-management-api/api/gen/proto/go/collector/v1"
)

// GetCollector retrieves a single collector by ID.
func (c *Client) GetCollector(ctx context.Context, id string) (*Collector, error) {
	req := &collectorv1.GetCollectorRequest{Id: id}

	resp, err := c.collector.GetCollector(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, connectErrToFleetErr(err, "GetCollector", id)
	}

	return collectorFromProto(resp.Msg), nil
}

// UpdateCollector updates an existing collector. As with the proto contract,
// updates are NOT selective: any attributes omitted from the supplied
// Collector will be cleared on the server. Callers should populate every
// field they want preserved.
func (c *Client) UpdateCollector(ctx context.Context, collector *Collector) (*Collector, error) {
	if collector == nil {
		return nil, fmt.Errorf("UpdateCollector: collector must not be nil")
	}

	req := &collectorv1.UpdateCollectorRequest{
		Collector: collectorToProto(collector),
	}

	resp, err := c.collector.UpdateCollector(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, connectErrToFleetErr(err, "UpdateCollector", collector.ID)
	}

	return collectorFromProto(resp.Msg), nil
}

// BulkUpdateCollectors applies a sequence of attribute mutations to multiple
// collectors atomically. All ops succeed or none do.
func (c *Client) BulkUpdateCollectors(ctx context.Context, ids []string, ops []*Operation) error {
	req := &collectorv1.BulkUpdateCollectorsRequest{
		Ids: ids,
		Ops: operationsToProto(ops),
	}

	_, err := c.collector.BulkUpdateCollectors(ctx, connect.NewRequest(req))
	if err != nil {
		return connectErrToFleetErr(err, "BulkUpdateCollectors", "")
	}

	return nil
}

// ListCollectors returns all collectors matching the supplied
// Prometheus-style matchers. An empty matchers slice returns every collector.
func (c *Client) ListCollectors(ctx context.Context, matchers []string) ([]*Collector, error) {
	req := &collectorv1.ListCollectorsRequest{Matchers: matchers}

	resp, err := c.collector.ListCollectors(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, connectErrToFleetErr(err, "ListCollectors", "")
	}

	protoCollectors := resp.Msg.GetCollectors()
	out := make([]*Collector, 0, len(protoCollectors))
	for _, pc := range protoCollectors {
		out = append(out, collectorFromProto(pc))
	}
	return out, nil
}
