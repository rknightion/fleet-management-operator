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
	"context"
	"errors"
	"maps"
	"net/http"
	"slices"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"

	fleetmanagementv1alpha1 "github.com/grafana/fleet-management-operator/api/v1alpha1"
	"github.com/grafana/fleet-management-operator/pkg/fleetclient"
)

// collectorMock is the package-level mock used by the suite-managed
// CollectorReconciler. Tests configure it via the `notRegistered` map and
// `bulkUpdateError` field, and inspect it via the call-count getters. A
// mutex guards the fields because the controller calls into the mock from a
// goroutine.
var collectorMock *mockFleetCollectorClient

// mockFleetCollectorClient is an in-memory FleetCollectorClient implementation
// for envtest. By default every collector ID is treated as registered and
// every BulkUpdateCollectors succeeds; tests opt into 404 / error behaviors
// per-ID.
type mockFleetCollectorClient struct {
	mu sync.Mutex

	// collectors holds the in-memory state. The mock auto-creates an entry
	// on first GetCollector for an unknown ID so the default test path is
	// "collector is registered".
	collectors map[string]*fleetclient.Collector

	// notRegistered is the set of IDs for which GetCollector returns 404.
	notRegistered map[string]bool

	// bulkUpdateErr lets tests inject an error from BulkUpdateCollectors.
	bulkUpdateErr error

	// getErr lets tests inject a non-404 error from GetCollector.
	getErr error

	// listResult is what ListCollectors returns. The CollectorDiscovery
	// reconciler also uses this mock through the FleetDiscoveryClient
	// interface; tests set listResult explicitly per scenario.
	listResult []*fleetclient.Collector

	// listErr lets tests inject an error from ListCollectors.
	listErr error

	// Call counters and last-request capture.
	callCountGet        int
	callCountBulkUpdate int
	callCountList       int
	lastBulkUpdateIDs   []string
	lastBulkUpdateOps   []*fleetclient.Operation
	lastListMatchers    []string
}

func newMockFleetCollectorClient() *mockFleetCollectorClient {
	return &mockFleetCollectorClient{
		collectors:    make(map[string]*fleetclient.Collector),
		notRegistered: make(map[string]bool),
	}
}

// reset returns the mock to a clean default state. Tests call this in
// BeforeEach so prior runs don't leak through.
func (m *mockFleetCollectorClient) reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.collectors = make(map[string]*fleetclient.Collector)
	m.notRegistered = make(map[string]bool)
	m.bulkUpdateErr = nil
	m.getErr = nil
	m.listResult = nil
	m.listErr = nil
	m.callCountGet = 0
	m.callCountBulkUpdate = 0
	m.callCountList = 0
	m.lastBulkUpdateIDs = nil
	m.lastBulkUpdateOps = nil
	m.lastListMatchers = nil
}

// markNotRegistered configures GetCollector to return 404 for the given id.
func (m *mockFleetCollectorClient) markNotRegistered(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.notRegistered[id] = true
}

// register pre-populates the in-memory collector with a starting value so
// tests can assert observed → desired diff behavior.
func (m *mockFleetCollectorClient) register(c *fleetclient.Collector) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := *c
	m.collectors[c.ID] = &cp
}

func (m *mockFleetCollectorClient) GetCollector(_ context.Context, id string) (*fleetclient.Collector, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.callCountGet++

	if m.getErr != nil {
		return nil, m.getErr
	}
	if m.notRegistered[id] {
		return nil, &fleetclient.FleetAPIError{
			StatusCode: http.StatusNotFound,
			Operation:  "GetCollector",
			Message:    "collector not registered",
		}
	}

	c, ok := m.collectors[id]
	if !ok {
		// Auto-register so the default path is "this collector exists".
		now := time.Now()
		c = &fleetclient.Collector{
			ID:               id,
			RemoteAttributes: map[string]string{},
			LocalAttributes:  map[string]string{"collector.os": "linux"},
			CollectorType:    "COLLECTOR_TYPE_ALLOY",
			CreatedAt:        &now,
			UpdatedAt:        &now,
		}
		m.collectors[id] = c
	}

	cp := *c
	if c.RemoteAttributes != nil {
		cp.RemoteAttributes = make(map[string]string, len(c.RemoteAttributes))
		maps.Copy(cp.RemoteAttributes, c.RemoteAttributes)
	}
	return &cp, nil
}

func (m *mockFleetCollectorClient) BulkUpdateCollectors(_ context.Context, ids []string, ops []*fleetclient.Operation) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.callCountBulkUpdate++
	m.lastBulkUpdateIDs = append([]string(nil), ids...)
	m.lastBulkUpdateOps = append([]*fleetclient.Operation(nil), ops...)

	if m.bulkUpdateErr != nil {
		return m.bulkUpdateErr
	}

	// Apply ops to in-memory state so subsequent GetCollector reflects them.
	for _, id := range ids {
		c, ok := m.collectors[id]
		if !ok {
			c = &fleetclient.Collector{ID: id, RemoteAttributes: map[string]string{}}
			m.collectors[id] = c
		}
		if c.RemoteAttributes == nil {
			c.RemoteAttributes = map[string]string{}
		}
		for _, op := range ops {
			key := stripRemoteAttrPath(op.Path)
			switch op.Op {
			case fleetclient.OpAdd, fleetclient.OpReplace:
				c.RemoteAttributes[key] = op.Value
			case fleetclient.OpRemove:
				delete(c.RemoteAttributes, key)
			}
		}
		now := time.Now()
		c.UpdatedAt = &now
	}
	return nil
}

// ListCollectors returns the test-configured listResult. Empty by
// default — tests must call setListResult to populate. The mock does
// not interpret matchers; tests set listResult to the already-filtered
// content they want the discovery reconciler to see.
func (m *mockFleetCollectorClient) ListCollectors(_ context.Context, matchers []string) ([]*fleetclient.Collector, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.callCountList++
	m.lastListMatchers = append([]string(nil), matchers...)

	if m.listErr != nil {
		return nil, m.listErr
	}

	out := make([]*fleetclient.Collector, 0, len(m.listResult))
	for _, c := range m.listResult {
		cp := *c
		if c.RemoteAttributes != nil {
			cp.RemoteAttributes = make(map[string]string, len(c.RemoteAttributes))
			maps.Copy(cp.RemoteAttributes, c.RemoteAttributes)
		}
		if c.LocalAttributes != nil {
			cp.LocalAttributes = make(map[string]string, len(c.LocalAttributes))
			maps.Copy(cp.LocalAttributes, c.LocalAttributes)
		}
		out = append(out, &cp)
	}
	return out, nil
}

// setListResult replaces the listResult slice. Used by discovery tests
// to control what ListCollectors returns on the next reconcile.
func (m *mockFleetCollectorClient) setListResult(collectors []*fleetclient.Collector) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.listResult = append([]*fleetclient.Collector(nil), collectors...)
}

// stripRemoteAttrPath is the inverse of attributes.remoteAttrPath, simplified
// for the mock (does not need to decode RFC 6901 escapes for the test inputs
// we use).
func stripRemoteAttrPath(path string) string {
	const prefix = "/remote_attributes/"
	if len(path) >= len(prefix) && path[:len(prefix)] == prefix {
		return path[len(prefix):]
	}
	return path
}

type statusCountingClient struct {
	client.Client
	statusUpdates int
}

type statusCountingWriter struct {
	client.StatusWriter
	updates *int
}

func (c *statusCountingClient) Status() client.StatusWriter {
	return &statusCountingWriter{
		StatusWriter: c.Client.Status(),
		updates:      &c.statusUpdates,
	}
}

func (w *statusCountingWriter) Update(ctx context.Context, obj client.Object, opts ...client.SubResourceUpdateOption) error {
	*w.updates++
	return w.StatusWriter.Update(ctx, obj, opts...)
}

var _ = Describe("Collector Controller", func() {
	const (
		collectorName      = "test-collector"
		collectorNamespace = "default"
		collectorID        = "edge-host-42"
		timeout            = 10 * time.Second
		interval           = 250 * time.Millisecond
	)

	typeNamespacedName := types.NamespacedName{
		Name:      collectorName,
		Namespace: collectorNamespace,
	}

	BeforeEach(func() {
		collectorMock.reset()
	})

	AfterEach(func() {
		collector := &fleetmanagementv1alpha1.Collector{}
		err := k8sClient.Get(context.Background(), typeNamespacedName, collector)
		if err == nil {
			Expect(k8sClient.Delete(context.Background(), collector)).To(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(context.Background(), typeNamespacedName, collector)
				return err != nil && apierrors.IsNotFound(err)
			}, timeout, interval).Should(BeTrue())
		}
	})

	It("adds a finalizer and syncs remote attributes on create", func() {
		ctx := context.Background()

		collector := &fleetmanagementv1alpha1.Collector{
			ObjectMeta: metav1.ObjectMeta{
				Name:      collectorName,
				Namespace: collectorNamespace,
			},
			Spec: fleetmanagementv1alpha1.CollectorSpec{
				ID: collectorID,
				RemoteAttributes: map[string]string{
					"env":    "prod",
					"region": "us-east-1",
				},
			},
		}
		Expect(k8sClient.Create(ctx, collector)).To(Succeed())

		By("Adding the finalizer")
		Eventually(func() bool {
			err := k8sClient.Get(ctx, typeNamespacedName, collector)
			if err != nil {
				return false
			}
			return slices.Contains(collector.Finalizers, collectorFinalizer)
		}, timeout, interval).Should(BeTrue())

		By("Reaching Ready=True")
		Eventually(func() bool {
			err := k8sClient.Get(ctx, typeNamespacedName, collector)
			if err != nil {
				return false
			}
			for _, c := range collector.Status.Conditions {
				if c.Type == conditionTypeReady && c.Status == metav1.ConditionTrue {
					return true
				}
			}
			return false
		}, timeout, interval).Should(BeTrue())

		By("Recording the keys it owns and the effective attributes")
		Expect(collector.Status.Registered).To(BeTrue())
		Expect(collector.Status.EffectiveRemoteAttributes).To(Equal(map[string]string{
			"env": "prod", "region": "us-east-1",
		}))
		owned := collectorMockOwnedKeys(collector)
		Expect(owned).To(ConsistOf("env", "region"))

		By("Issuing ADD operations to Fleet")
		ops := collectorMockLastOps()
		Expect(ops).To(HaveLen(2))
		for _, op := range ops {
			Expect(op.Op).To(Equal(fleetclient.OpAdd))
		}
	})

	It("transitions to NotRegistered when the collector has no Fleet record", func() {
		ctx := context.Background()
		collectorMock.markNotRegistered(collectorID)

		collector := &fleetmanagementv1alpha1.Collector{
			ObjectMeta: metav1.ObjectMeta{
				Name:      collectorName,
				Namespace: collectorNamespace,
			},
			Spec: fleetmanagementv1alpha1.CollectorSpec{
				ID: collectorID,
				RemoteAttributes: map[string]string{
					"env": "prod",
				},
			},
		}
		Expect(k8sClient.Create(ctx, collector)).To(Succeed())

		Eventually(func() string {
			err := k8sClient.Get(ctx, typeNamespacedName, collector)
			if err != nil {
				return ""
			}
			for _, c := range collector.Status.Conditions {
				if c.Type == conditionTypeReady {
					return c.Reason
				}
			}
			return ""
		}, timeout, interval).Should(Equal(collectorReasonNotRegistered))

		Expect(collector.Status.Registered).To(BeFalse())
	})

	It("skips unchanged NotRegistered status writes while preserving timed requeue", func() {
		ctx := context.Background()
		readyMessage := "Collector \"edge-host-42\" has not yet registered with Fleet Management"
		collector := &fleetmanagementv1alpha1.Collector{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "notregistered-noop",
				Namespace:  collectorNamespace,
				Generation: 3,
			},
			Spec: fleetmanagementv1alpha1.CollectorSpec{ID: collectorID},
			Status: fleetmanagementv1alpha1.CollectorStatus{
				ObservedGeneration: 3,
				Registered:         false,
				Conditions: []metav1.Condition{
					{
						Type:               conditionTypeReady,
						Status:             metav1.ConditionFalse,
						Reason:             collectorReasonNotRegistered,
						Message:            readyMessage,
						ObservedGeneration: 3,
					},
					{
						Type:               conditionTypeSynced,
						Status:             metav1.ConditionFalse,
						Reason:             collectorReasonNotRegistered,
						Message:            "Awaiting collector registration",
						ObservedGeneration: 3,
					},
				},
			},
		}
		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme.Scheme).
			WithStatusSubresource(&fleetmanagementv1alpha1.Collector{}).
			WithObjects(collector).
			Build()
		countingClient := &statusCountingClient{Client: fakeClient}
		reconciler := &CollectorReconciler{Client: countingClient, Scheme: scheme.Scheme}

		result, err := reconciler.updateStatusNotRegistered(ctx, collector)

		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(Equal(notRegisteredRequeueAfter))
		Expect(countingClient.statusUpdates).To(Equal(0))
	})

	It("skips identical error status writes while preserving exponential backoff", func() {
		ctx := context.Background()
		originalErr := errors.New("temporary Fleet Management outage")
		formatted := formatConditionMessage(collectorReasonSyncFailed, originalErr)
		collector := &fleetmanagementv1alpha1.Collector{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "error-noop",
				Namespace:  collectorNamespace,
				Generation: 5,
			},
			Spec: fleetmanagementv1alpha1.CollectorSpec{ID: collectorID},
			Status: fleetmanagementv1alpha1.CollectorStatus{
				ObservedGeneration: 5,
				Conditions: []metav1.Condition{
					{
						Type:               conditionTypeReady,
						Status:             metav1.ConditionFalse,
						Reason:             collectorReasonSyncFailed,
						Message:            formatted,
						ObservedGeneration: 5,
					},
					{
						Type:               conditionTypeSynced,
						Status:             metav1.ConditionFalse,
						Reason:             collectorReasonSyncFailed,
						Message:            formatted,
						ObservedGeneration: 5,
					},
				},
			},
		}
		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme.Scheme).
			WithStatusSubresource(&fleetmanagementv1alpha1.Collector{}).
			WithObjects(collector).
			Build()
		countingClient := &statusCountingClient{Client: fakeClient}
		reconciler := &CollectorReconciler{Client: countingClient, Scheme: scheme.Scheme}

		var outcome string
		result, err := reconciler.updateStatusError(ctx, collector, collectorReasonSyncFailed, originalErr, &outcome)

		Expect(err).To(Equal(originalErr))
		Expect(result).To(Equal(ctrl.Result{}))
		Expect(outcome).To(Equal(collectorReasonSyncFailed))
		Expect(countingClient.statusUpdates).To(Equal(0))
	})

	It("emits REMOVE operations on delete for keys it owns", func() {
		ctx := context.Background()

		collector := &fleetmanagementv1alpha1.Collector{
			ObjectMeta: metav1.ObjectMeta{
				Name:      collectorName,
				Namespace: collectorNamespace,
			},
			Spec: fleetmanagementv1alpha1.CollectorSpec{
				ID:               collectorID,
				RemoteAttributes: map[string]string{"env": "prod"},
			},
		}
		Expect(k8sClient.Create(ctx, collector)).To(Succeed())

		Eventually(func() bool {
			err := k8sClient.Get(ctx, typeNamespacedName, collector)
			if err != nil {
				return false
			}
			return collector.Status.Registered && len(collector.Status.AttributeOwners) > 0
		}, timeout, interval).Should(BeTrue())

		// Snapshot pre-delete BulkUpdate count so we can prove a fresh call
		// happens during deletion.
		preDeleteCount := collectorMockBulkUpdateCount()

		Expect(k8sClient.Delete(ctx, collector)).To(Succeed())

		Eventually(func() bool {
			err := k8sClient.Get(ctx, typeNamespacedName, collector)
			return err != nil && apierrors.IsNotFound(err)
		}, timeout, interval).Should(BeTrue())

		Expect(collectorMockBulkUpdateCount()).To(BeNumerically(">", preDeleteCount),
			"expected a BulkUpdateCollectors call during delete reconciliation")
		removeOps := collectorMockLastOps()
		Expect(removeOps).NotTo(BeEmpty())
		for _, op := range removeOps {
			Expect(op.Op).To(Equal(fleetclient.OpRemove))
		}
	})
})

var _ = Describe("Collector cross-layer watches", func() {
	It("suppresses status-only Policy and ExternalAttributeSync updates", func() {
		oldPolicy := &fleetmanagementv1alpha1.RemoteAttributePolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "policy", Namespace: "default", Generation: 1},
			Status: fleetmanagementv1alpha1.RemoteAttributePolicyStatus{
				MatchedCollectorIDs: []string{"edge-1"},
			},
		}
		newPolicy := oldPolicy.DeepCopy()
		newPolicy.Status.MatchedCollectorIDs = []string{"edge-1", "edge-2"}
		Expect(collectorPolicyWatchPredicate().Update(event.UpdateEvent{
			ObjectOld: oldPolicy,
			ObjectNew: newPolicy,
		})).To(BeFalse())

		newPolicySpec := oldPolicy.DeepCopy()
		newPolicySpec.Generation = 2
		Expect(collectorPolicyWatchPredicate().Update(event.UpdateEvent{
			ObjectOld: oldPolicy,
			ObjectNew: newPolicySpec,
		})).To(BeTrue())

		oldSync := &fleetmanagementv1alpha1.ExternalAttributeSync{
			ObjectMeta: metav1.ObjectMeta{Name: "sync", Namespace: "default", Generation: 1},
			Status: fleetmanagementv1alpha1.ExternalAttributeSyncStatus{
				RecordsSeen: 1,
				OwnedKeys: []fleetmanagementv1alpha1.OwnedKeyEntry{
					{CollectorID: "edge-1", Attributes: map[string]string{"env": "prod"}},
				},
			},
		}
		newSyncStatusOnly := oldSync.DeepCopy()
		newSyncStatusOnly.Status.RecordsSeen = 2
		Expect(collectorExternalSyncWatchPredicate().Update(event.UpdateEvent{
			ObjectOld: oldSync,
			ObjectNew: newSyncStatusOnly,
		})).To(BeFalse())

		newSyncOwnedKeys := oldSync.DeepCopy()
		newSyncOwnedKeys.Status.OwnedKeys = []fleetmanagementv1alpha1.OwnedKeyEntry{
			{CollectorID: "edge-2", Attributes: map[string]string{"env": "prod"}},
		}
		Expect(collectorExternalSyncWatchPredicate().Update(event.UpdateEvent{
			ObjectOld: oldSync,
			ObjectNew: newSyncOwnedKeys,
		})).To(BeTrue())
	})

	It("maps ExternalAttributeSync events only to collectors named in ownedKeys", func() {
		ctx := context.Background()
		collectors := []client.Object{
			&fleetmanagementv1alpha1.Collector{
				ObjectMeta: metav1.ObjectMeta{Name: "one", Namespace: "default"},
				Spec:       fleetmanagementv1alpha1.CollectorSpec{ID: "edge-1"},
			},
			&fleetmanagementv1alpha1.Collector{
				ObjectMeta: metav1.ObjectMeta{Name: "two", Namespace: "default"},
				Spec:       fleetmanagementv1alpha1.CollectorSpec{ID: "edge-2"},
			},
			&fleetmanagementv1alpha1.Collector{
				ObjectMeta: metav1.ObjectMeta{Name: "three", Namespace: "default"},
				Spec:       fleetmanagementv1alpha1.CollectorSpec{ID: "edge-3"},
			},
		}
		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme.Scheme).
			WithObjects(collectors...).
			Build()

		r := &CollectorReconciler{Client: fakeClient, Scheme: scheme.Scheme}
		sync := &fleetmanagementv1alpha1.ExternalAttributeSync{
			ObjectMeta: metav1.ObjectMeta{Name: "sync", Namespace: "default"},
			Status: fleetmanagementv1alpha1.ExternalAttributeSyncStatus{
				OwnedKeys: []fleetmanagementv1alpha1.OwnedKeyEntry{
					{CollectorID: "edge-2", Attributes: map[string]string{"env": "prod"}},
					{CollectorID: "missing", Attributes: map[string]string{"env": "prod"}},
				},
			},
		}

		Expect(r.collectorsTouchedBySync(ctx, sync)).To(ConsistOf(reconcileRequest("default", "two")))
	})

	It("ignores Truncated ExternalAttributeSync handoffs when building the external layer", func() {
		ctx := context.Background()
		collector := &fleetmanagementv1alpha1.Collector{
			ObjectMeta: metav1.ObjectMeta{Name: "edge", Namespace: "default"},
			Spec:       fleetmanagementv1alpha1.CollectorSpec{ID: "edge-1"},
		}
		sync := &fleetmanagementv1alpha1.ExternalAttributeSync{
			ObjectMeta: metav1.ObjectMeta{Name: "sync", Namespace: "default"},
			Status: fleetmanagementv1alpha1.ExternalAttributeSyncStatus{
				OwnedKeys: []fleetmanagementv1alpha1.OwnedKeyEntry{
					{CollectorID: "edge-1", Attributes: map[string]string{"env": "prod"}},
				},
				Conditions: []metav1.Condition{
					{
						Type:   externalSyncConditionTruncated,
						Status: metav1.ConditionTrue,
						Reason: externalSyncReasonOwnedKeysExceeded,
					},
				},
			},
		}
		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme.Scheme).
			WithObjects(collector, sync).
			Build()

		r := &CollectorReconciler{Client: fakeClient, Scheme: scheme.Scheme}
		layer, err := r.externalSyncLayerForCollector(ctx, collector)

		Expect(err).NotTo(HaveOccurred())
		Expect(layer.Attrs).To(BeEmpty())
	})

	It("periodically resyncs and removes keys from deleted layers", func() {
		ctx := context.Background()
		mock := newMockFleetCollectorClient()
		mock.register(&fleetclient.Collector{
			ID:               "edge-1",
			RemoteAttributes: map[string]string{"env": "prod"},
			LocalAttributes:  map[string]string{"collector.os": "linux"},
			CollectorType:    "COLLECTOR_TYPE_ALLOY",
		})

		collector := &fleetmanagementv1alpha1.Collector{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "edge",
				Namespace:  "default",
				Finalizers: []string{collectorFinalizer},
			},
			Spec: fleetmanagementv1alpha1.CollectorSpec{ID: "edge-1"},
			Status: fleetmanagementv1alpha1.CollectorStatus{
				ObservedGeneration: 1,
				AttributeOwners: []fleetmanagementv1alpha1.AttributeOwnership{
					{
						Key:       "env",
						OwnerKind: fleetmanagementv1alpha1.AttributeOwnerExternalAttributeSync,
						OwnerName: "default/sync",
						Value:     "prod",
					},
				},
			},
		}
		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme.Scheme).
			WithStatusSubresource(&fleetmanagementv1alpha1.Collector{}).
			WithObjects(collector).
			Build()

		r := &CollectorReconciler{
			Client:      fakeClient,
			Scheme:      scheme.Scheme,
			FleetClient: mock,
		}

		result, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{
			Namespace: "default",
			Name:      "edge",
		}})

		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(Equal(collectorPeriodicResyncAfter))
		Expect(mock.callCountBulkUpdate).To(Equal(1))
		Expect(mock.lastBulkUpdateOps).To(HaveLen(1))
		Expect(mock.lastBulkUpdateOps[0].Op).To(Equal(fleetclient.OpRemove))
		Expect(mock.lastBulkUpdateOps[0].Path).To(Equal("/remote_attributes/env"))
	})
})

func reconcileRequest(namespace, name string) ctrl.Request {
	return ctrl.Request{NamespacedName: types.NamespacedName{Namespace: namespace, Name: name}}
}

// collectorMockOwnedKeys returns the keys from the mock's most recent
// reconciliation that the suite-level CollectorReconciler claimed ownership
// of.
func collectorMockOwnedKeys(c *fleetmanagementv1alpha1.Collector) []string {
	out := make([]string, 0, len(c.Status.AttributeOwners))
	for _, o := range c.Status.AttributeOwners {
		if o.OwnerKind == fleetmanagementv1alpha1.AttributeOwnerCollector {
			out = append(out, o.Key)
		}
	}
	return out
}

// collectorMockLastOps reads the captured ops under lock so the test does
// not race with the reconciler goroutine.
func collectorMockLastOps() []*fleetclient.Operation {
	collectorMock.mu.Lock()
	defer collectorMock.mu.Unlock()
	return append([]*fleetclient.Operation(nil), collectorMock.lastBulkUpdateOps...)
}

// collectorMockBulkUpdateCount returns the call count under lock.
func collectorMockBulkUpdateCount() int {
	collectorMock.mu.Lock()
	defer collectorMock.mu.Unlock()
	return collectorMock.callCountBulkUpdate
}
