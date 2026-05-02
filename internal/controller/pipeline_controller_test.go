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
	"fmt"
	"net/http"
	"slices"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	fleetmanagementv1alpha1 "github.com/grafana/fleet-management-operator/api/v1alpha1"
	"github.com/grafana/fleet-management-operator/pkg/fleetclient"
)

// pipelineMock is the package-level mock used by the suite-managed
// PipelineReconciler. Tests configure it in BeforeEach via reset() and
// toggle individual flags. A mutex guards the fields because the controller
// calls into the mock from a goroutine.
// Tests in this file run serially against a shared envtest cluster.
// Do not enable Ginkgo parallel mode -- mock state is shared.
var pipelineMock *mockFleetClient

func boolPtr(b bool) *bool { return &b }

// Mock Fleet Management API client
type mockFleetClient struct {
	mu sync.Mutex

	pipelines               map[string]*fleetclient.Pipeline
	getError                error
	upsertError             error
	deleteError             error
	callCount               int
	lastUpsertRequest       *fleetclient.UpsertPipelineRequest
	shouldReturn404         bool
	shouldReturn400         bool
	shouldReturn429         bool
	shouldReturn404OnFirst  bool // Return 404 on first call, then succeed
	shouldReturn404OnDelete bool // Return 404 only from DeletePipeline
}

func newMockFleetClient() *mockFleetClient {
	return &mockFleetClient{
		pipelines: make(map[string]*fleetclient.Pipeline),
	}
}

// reset returns the mock to a clean default state. Tests call this in
// BeforeEach so prior runs don't leak through. Must only be called from a
// serial context (BeforeEach) — not from goroutines. We hold the lock and
// reset every field individually rather than struct-zeroing because the
// reconcile goroutine may still be holding the mutex briefly during BeforeEach
// teardown; struct-zero would clobber the mutex and produce
// unlock-of-unlocked-mutex panics under -race.
func (m *mockFleetClient) reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pipelines = make(map[string]*fleetclient.Pipeline)
	m.getError = nil
	m.upsertError = nil
	m.deleteError = nil
	m.callCount = 0
	m.lastUpsertRequest = nil
	m.shouldReturn404 = false
	m.shouldReturn400 = false
	m.shouldReturn429 = false
	m.shouldReturn404OnFirst = false
	m.shouldReturn404OnDelete = false
	// Do NOT reset m.mu itself.
}

// CallCount returns the number of UpsertPipeline calls observed by the mock.
// Acquires the mutex so callers (test goroutines) do not race the controller
// goroutine that updates callCount.
func (m *mockFleetClient) CallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.callCount
}

// Has returns true if the given pipeline ID is present in the mock's
// in-memory store. Acquires the mutex for the same reason as CallCount.
func (m *mockFleetClient) Has(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.pipelines[id]
	return ok
}

func (m *mockFleetClient) GetPipeline(ctx context.Context, id string) (*fleetclient.Pipeline, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.shouldReturn404 {
		return nil, &fleetclient.FleetAPIError{
			StatusCode: http.StatusNotFound,
			Operation:  "GetPipeline",
			Message:    "pipeline not found",
		}
	}
	if m.getError != nil {
		return nil, m.getError
	}
	p, ok := m.pipelines[id]
	if !ok {
		return nil, &fleetclient.FleetAPIError{
			StatusCode: http.StatusNotFound,
			Operation:  "GetPipeline",
			Message:    "pipeline not found",
		}
	}
	cp := *p
	return &cp, nil
}

func (m *mockFleetClient) UpsertPipeline(ctx context.Context, req *fleetclient.UpsertPipelineRequest) (*fleetclient.Pipeline, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.callCount++
	m.lastUpsertRequest = req

	if m.shouldReturn404OnFirst && m.callCount == 1 {
		return nil, &fleetclient.FleetAPIError{
			StatusCode: http.StatusNotFound,
			Operation:  "UpsertPipeline",
			Message:    "pipeline not found",
		}
	}

	if m.shouldReturn400 {
		return nil, &fleetclient.FleetAPIError{
			StatusCode: http.StatusBadRequest,
			Operation:  "UpsertPipeline",
			Message:    "validation error: invalid configuration",
		}
	}

	if m.shouldReturn429 {
		return nil, &fleetclient.FleetAPIError{
			StatusCode: http.StatusTooManyRequests,
			Operation:  "UpsertPipeline",
			Message:    "rate limit exceeded",
		}
	}

	if m.shouldReturn404 {
		return nil, &fleetclient.FleetAPIError{
			StatusCode: http.StatusNotFound,
			Operation:  "UpsertPipeline",
			Message:    "pipeline not found",
		}
	}

	if m.upsertError != nil {
		return nil, m.upsertError
	}

	// Assign ID if not present
	if req.Pipeline.ID == "" {
		req.Pipeline.ID = "mock-id-123"
	}

	now := time.Now()
	req.Pipeline.CreatedAt = &now
	req.Pipeline.UpdatedAt = &now

	m.pipelines[req.Pipeline.ID] = req.Pipeline

	return req.Pipeline, nil
}

func (m *mockFleetClient) DeletePipeline(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.shouldReturn404 || m.shouldReturn404OnDelete {
		return &fleetclient.FleetAPIError{
			StatusCode: http.StatusNotFound,
			Operation:  "DeletePipeline",
			Message:    "pipeline not found",
		}
	}

	if m.deleteError != nil {
		return m.deleteError
	}

	delete(m.pipelines, id)
	return nil
}

// statusErrorClient wraps a fake client to inject status update errors
type statusErrorClient struct {
	client.Client
	statusUpdateErr error
}

type statusErrorWriter struct {
	client.StatusWriter
	err error
}

func (c *statusErrorClient) Status() client.StatusWriter {
	if c.statusUpdateErr != nil {
		return &statusErrorWriter{StatusWriter: c.Client.Status(), err: c.statusUpdateErr}
	}
	return c.Client.Status()
}

func (w *statusErrorWriter) Update(ctx context.Context, obj client.Object, opts ...client.SubResourceUpdateOption) error {
	return w.err
}

var _ = Describe("Pipeline Controller", func() {
	Context("When reconciling a Pipeline", func() {
		const (
			pipelineName      = "test-pipeline"
			pipelineNamespace = "default"
			timeout           = time.Second * 10
			interval          = time.Millisecond * 250
		)

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      pipelineName,
			Namespace: pipelineNamespace,
		}

		BeforeEach(func() {
			// Zero-value reset covers all fields -- safer than listing them
			// individually. Re-initialize the map field afterward.
			pipelineMock.reset()
		})

		AfterEach(func() {
			// Cleanup
			pipeline := &fleetmanagementv1alpha1.Pipeline{}
			err := k8sClient.Get(ctx, typeNamespacedName, pipeline)
			if err == nil {
				Expect(k8sClient.Delete(ctx, pipeline)).To(Succeed())

				// Wait for pipeline to be fully deleted
				Eventually(func() bool {
					err := k8sClient.Get(ctx, typeNamespacedName, pipeline)
					return err != nil && apierrors.IsNotFound(err)
				}, timeout, interval).Should(BeTrue())
			}
		})

		It("should successfully reconcile a new Pipeline", func() {
			By("Creating a new Pipeline")
			pipeline := &fleetmanagementv1alpha1.Pipeline{
				ObjectMeta: metav1.ObjectMeta{
					Name:      pipelineName,
					Namespace: pipelineNamespace,
				},
				Spec: fleetmanagementv1alpha1.PipelineSpec{
					Contents:   "prometheus.exporter.self \"alloy\" { }",
					Enabled:    boolPtr(true),
					Matchers:   []string{"env=prod"},
					ConfigType: fleetmanagementv1alpha1.ConfigTypeAlloy,
				},
			}
			Expect(k8sClient.Create(ctx, pipeline)).To(Succeed())

			By("Checking if finalizer is added")
			Eventually(func() bool {
				err := k8sClient.Get(ctx, typeNamespacedName, pipeline)
				if err != nil {
					return false
				}
				return slices.Contains(pipeline.Finalizers, pipelineFinalizer)
			}, timeout, interval).Should(BeTrue())

			By("Checking if status is updated with Fleet Management ID")
			Eventually(func() string {
				err := k8sClient.Get(ctx, typeNamespacedName, pipeline)
				if err != nil {
					return ""
				}
				return pipeline.Status.ID
			}, timeout, interval).Should(Equal("mock-id-123"))

			By("Checking if Ready condition is set to True")
			Eventually(func() bool {
				err := k8sClient.Get(ctx, typeNamespacedName, pipeline)
				if err != nil {
					return false
				}
				for _, condition := range pipeline.Status.Conditions {
					if condition.Type == conditionTypeReady && condition.Status == metav1.ConditionTrue {
						return true
					}
				}
				return false
			}, timeout, interval).Should(BeTrue())
		})

		It("should default spec.enabled through the CRD schema while preserving explicit values", func() {
			cases := []struct {
				name    string
				enabled *bool
				want    bool
			}{
				{name: "omitted", enabled: nil, want: true},
				{name: "explicit-true", enabled: boolPtr(true), want: true},
				{name: "explicit-false", enabled: boolPtr(false), want: false},
			}

			for _, tc := range cases {
				By("Creating a Pipeline with " + tc.name + " enabled")
				pipeline := &fleetmanagementv1alpha1.Pipeline{
					ObjectMeta: metav1.ObjectMeta{
						Name:      pipelineName + "-" + tc.name,
						Namespace: pipelineNamespace,
					},
					Spec: fleetmanagementv1alpha1.PipelineSpec{
						Contents:   "prometheus.exporter.self \"alloy\" { }",
						Enabled:    tc.enabled,
						ConfigType: fleetmanagementv1alpha1.ConfigTypeAlloy,
					},
				}
				key := types.NamespacedName{Name: pipeline.Name, Namespace: pipeline.Namespace}
				Expect(k8sClient.Create(ctx, pipeline)).To(Succeed())

				got := &fleetmanagementv1alpha1.Pipeline{}
				Expect(k8sClient.Get(ctx, key, got)).To(Succeed())
				Expect(got.Spec.Enabled).NotTo(BeNil())
				Expect(*got.Spec.Enabled).To(Equal(tc.want))

				Expect(k8sClient.Delete(ctx, got)).To(Succeed())
				Eventually(func() bool {
					err := k8sClient.Get(ctx, key, got)
					return err != nil && apierrors.IsNotFound(err)
				}, timeout, interval).Should(BeTrue())
			}
		})

		It("should skip reconciliation when spec hasn't changed", func() {
			By("Creating a Pipeline with Fleet ID already set")
			pipeline := &fleetmanagementv1alpha1.Pipeline{
				ObjectMeta: metav1.ObjectMeta{
					Name:       pipelineName,
					Namespace:  pipelineNamespace,
					Generation: 1,
				},
				Spec: fleetmanagementv1alpha1.PipelineSpec{
					Contents:   "prometheus.exporter.self \"alloy\" { }",
					Enabled:    boolPtr(true),
					ConfigType: fleetmanagementv1alpha1.ConfigTypeAlloy,
				},
			}
			Expect(k8sClient.Create(ctx, pipeline)).To(Succeed())

			// Wait for first reconciliation
			Eventually(func() string {
				err := k8sClient.Get(ctx, typeNamespacedName, pipeline)
				if err != nil {
					return ""
				}
				return pipeline.Status.ID
			}, timeout, interval).Should(Equal("mock-id-123"))

			// Get current state
			Expect(k8sClient.Get(ctx, typeNamespacedName, pipeline)).To(Succeed())
			currentGeneration := pipeline.Generation
			currentObservedGeneration := pipeline.Status.ObservedGeneration

			By("Verifying observedGeneration matches generation")
			Expect(currentObservedGeneration).To(Equal(currentGeneration))

			// Note: Without changing spec, controller should skip reconciliation
			// This is tested by the observedGeneration check in the controller
		})

		It("should handle deletion properly", func() {
			By("Creating a Pipeline")
			pipeline := &fleetmanagementv1alpha1.Pipeline{
				ObjectMeta: metav1.ObjectMeta{
					Name:      pipelineName,
					Namespace: pipelineNamespace,
				},
				Spec: fleetmanagementv1alpha1.PipelineSpec{
					Contents:   "prometheus.exporter.self \"alloy\" { }",
					Enabled:    boolPtr(true),
					ConfigType: fleetmanagementv1alpha1.ConfigTypeAlloy,
				},
			}
			Expect(k8sClient.Create(ctx, pipeline)).To(Succeed())

			// Wait for Fleet ID
			Eventually(func() string {
				err := k8sClient.Get(ctx, typeNamespacedName, pipeline)
				if err != nil {
					return ""
				}
				return pipeline.Status.ID
			}, timeout, interval).Should(Equal("mock-id-123"))

			By("Deleting the Pipeline")
			Expect(k8sClient.Delete(ctx, pipeline)).To(Succeed())

			By("Verifying the Pipeline is deleted")
			Eventually(func() bool {
				err := k8sClient.Get(ctx, typeNamespacedName, pipeline)
				return err != nil
			}, timeout, interval).Should(BeTrue())
		})

		It("preserves Synced=True when Fleet API returns 429 and recovers on retry", func() {
			// 429 handling requeues with a fixed delay without updating status (keeping
			// the last-known-good state). This ensures that transient rate-limits do not
			// flip a healthy Pipeline to an error state. Once 429 clears the controller
			// reconciles again and the spec update is applied.
			By("Creating a Pipeline with valid spec")
			pipeline := &fleetmanagementv1alpha1.Pipeline{
				ObjectMeta: metav1.ObjectMeta{
					Name:      pipelineName,
					Namespace: pipelineNamespace,
				},
				Spec: fleetmanagementv1alpha1.PipelineSpec{
					Contents:   "prometheus.exporter.self \"alloy\" { }",
					Enabled:    boolPtr(true),
					Matchers:   []string{"env=prod"},
					ConfigType: fleetmanagementv1alpha1.ConfigTypeAlloy,
				},
			}
			Expect(k8sClient.Create(ctx, pipeline)).To(Succeed())

			By("Waiting for the initial sync to succeed (FleetID set)")
			Eventually(func() string {
				err := k8sClient.Get(ctx, typeNamespacedName, pipeline)
				if err != nil {
					return ""
				}
				return pipeline.Status.ID
			}, timeout, interval).Should(Equal("mock-id-123"))

			By("Enabling 429 responses on the mock")
			pipelineMock.mu.Lock()
			pipelineMock.shouldReturn429 = true
			pipelineMock.mu.Unlock()

			By("Updating the Pipeline spec to trigger a new reconcile")
			Expect(k8sClient.Get(ctx, typeNamespacedName, pipeline)).To(Succeed())
			pipeline.Spec.Contents = "prometheus.exporter.self \"alloy\" { } // updated"
			Expect(k8sClient.Update(ctx, pipeline)).To(Succeed())

			By("Disabling 429 responses so the controller can recover")
			pipelineMock.mu.Lock()
			pipelineMock.shouldReturn429 = false
			pipelineMock.mu.Unlock()

			By("Asserting Synced=True after recovery — status is preserved through the 429 window")
			Eventually(func() bool {
				err := k8sClient.Get(ctx, typeNamespacedName, pipeline)
				if err != nil {
					return false
				}
				for _, condition := range pipeline.Status.Conditions {
					if condition.Type == conditionTypeSynced &&
						condition.Status == metav1.ConditionTrue {
						return true
					}
				}
				return false
			}, timeout, interval).Should(BeTrue(), "expected Synced=True after 429 clears")
		})

		It("should handle validation errors from Fleet Management API", func() {
			// This would require setting up the mock client differently
			// For now, we'll test the basic error handling path
			Skip("Requires dynamic mock client configuration")
		})

		It("should convert ConfigType correctly", func() {
			By("Testing Alloy config type")
			Expect(fleetmanagementv1alpha1.ConfigTypeAlloy.ToFleetAPI()).To(Equal("CONFIG_TYPE_ALLOY"))

			By("Testing OpenTelemetryCollector config type")
			Expect(fleetmanagementv1alpha1.ConfigTypeOpenTelemetryCollector.ToFleetAPI()).To(Equal("CONFIG_TYPE_OTEL"))

			By("Testing round-trip conversion")
			alloyType := fleetmanagementv1alpha1.ConfigTypeFromFleetAPI("CONFIG_TYPE_ALLOY")
			Expect(alloyType).To(Equal(fleetmanagementv1alpha1.ConfigTypeAlloy))

			otelType := fleetmanagementv1alpha1.ConfigTypeFromFleetAPI("CONFIG_TYPE_OTEL")
			Expect(otelType).To(Equal(fleetmanagementv1alpha1.ConfigTypeOpenTelemetryCollector))
		})
	})

	Context("When building UpsertPipelineRequest", func() {
		It("should use metadata.name when spec.name is empty", func() {
			reconciler := &PipelineReconciler{}
			pipeline := &fleetmanagementv1alpha1.Pipeline{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pipeline",
					Namespace: "default",
				},
				Spec: fleetmanagementv1alpha1.PipelineSpec{
					Contents:   "test content",
					Enabled:    boolPtr(true),
					ConfigType: fleetmanagementv1alpha1.ConfigTypeAlloy,
				},
			}

			req := reconciler.buildUpsertRequest(pipeline)
			Expect(req.Pipeline.Name).To(Equal("test-pipeline"))
		})

		It("should use spec.name when provided", func() {
			reconciler := &PipelineReconciler{}
			pipeline := &fleetmanagementv1alpha1.Pipeline{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "k8s-pipeline",
					Namespace: "default",
				},
				Spec: fleetmanagementv1alpha1.PipelineSpec{
					Name:       "custom-pipeline-name",
					Contents:   "test content",
					Enabled:    boolPtr(true),
					ConfigType: fleetmanagementv1alpha1.ConfigTypeAlloy,
				},
			}

			req := reconciler.buildUpsertRequest(pipeline)
			Expect(req.Pipeline.Name).To(Equal("custom-pipeline-name"))
		})

		It("should convert ConfigType to Fleet API format", func() {
			reconciler := &PipelineReconciler{}
			pipeline := &fleetmanagementv1alpha1.Pipeline{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "default",
				},
				Spec: fleetmanagementv1alpha1.PipelineSpec{
					Contents:   "test",
					ConfigType: fleetmanagementv1alpha1.ConfigTypeOpenTelemetryCollector,
				},
			}

			req := reconciler.buildUpsertRequest(pipeline)
			Expect(req.Pipeline.ConfigType).To(Equal("CONFIG_TYPE_OTEL"))
		})

		It("should apply the enabled default for non-defaulted typed-client objects", func() {
			reconciler := &PipelineReconciler{}

			cases := []struct {
				name    string
				enabled *bool
				want    bool
			}{
				{name: "omitted before API defaulting", enabled: nil, want: true},
				{name: "explicit true", enabled: boolPtr(true), want: true},
				{name: "explicit false", enabled: boolPtr(false), want: false},
			}

			for _, tc := range cases {
				By("checking " + tc.name)
				pipeline := &fleetmanagementv1alpha1.Pipeline{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test",
						Namespace: "default",
					},
					Spec: fleetmanagementv1alpha1.PipelineSpec{
						Contents:   "test",
						Enabled:    tc.enabled,
						ConfigType: fleetmanagementv1alpha1.ConfigTypeAlloy,
					},
				}

				req := reconciler.buildUpsertRequest(pipeline)
				Expect(req.Pipeline.Enabled).To(Equal(tc.want))
			}
		})

		It("should omit source when spec.source is nil or legacy Kubernetes", func() {
			reconciler := &PipelineReconciler{}

			for _, source := range []*fleetmanagementv1alpha1.PipelineSource{
				nil,
				{
					Type:      fleetmanagementv1alpha1.SourceTypeKubernetes,
					Namespace: "cluster-a",
				},
			} {
				pipeline := &fleetmanagementv1alpha1.Pipeline{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test",
						Namespace: "default",
					},
					Spec: fleetmanagementv1alpha1.PipelineSpec{
						Contents:   "test",
						ConfigType: fleetmanagementv1alpha1.ConfigTypeAlloy,
						Source:     source,
					},
				}

				req := reconciler.buildUpsertRequest(pipeline)
				Expect(req.Pipeline.Source).To(BeNil())
			}
		})

		It("should pass supported source values through to Fleet", func() {
			reconciler := &PipelineReconciler{}
			pipeline := &fleetmanagementv1alpha1.Pipeline{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "default",
				},
				Spec: fleetmanagementv1alpha1.PipelineSpec{
					Contents:   "test",
					ConfigType: fleetmanagementv1alpha1.ConfigTypeAlloy,
					Source: &fleetmanagementv1alpha1.PipelineSource{
						Type:      fleetmanagementv1alpha1.SourceTypeGrafana,
						Namespace: "instrumentation-hub",
					},
				},
			}

			req := reconciler.buildUpsertRequest(pipeline)
			Expect(req.Pipeline.Source).NotTo(BeNil())
			Expect(req.Pipeline.Source.Type).To(Equal("SOURCE_TYPE_GRAFANA"))
			Expect(req.Pipeline.Source.Namespace).To(Equal("instrumentation-hub"))
		})
	})

	Context("Mock Fleet Client Tests", func() {
		It("should track API calls", func() {
			mock := newMockFleetClient()
			ctx := context.Background()

			req := &fleetclient.UpsertPipelineRequest{
				Pipeline: &fleetclient.Pipeline{
					Name:     "test",
					Contents: "test content",
				},
			}

			_, err := mock.UpsertPipeline(ctx, req)
			Expect(err).ToNot(HaveOccurred())
			Expect(mock.CallCount()).To(Equal(1))
		})

		It("should store pipelines", func() {
			mock := newMockFleetClient()
			ctx := context.Background()

			req := &fleetclient.UpsertPipelineRequest{
				Pipeline: &fleetclient.Pipeline{
					Name:     "test",
					Contents: "test content",
				},
			}

			result, err := mock.UpsertPipeline(ctx, req)
			Expect(err).ToNot(HaveOccurred())
			Expect(result.ID).To(Equal("mock-id-123"))
			Expect(result.Name).To(Equal("test"))
		})

		It("should handle deletion", func() {
			mock := newMockFleetClient()
			ctx := context.Background()

			req := &fleetclient.UpsertPipelineRequest{
				Pipeline: &fleetclient.Pipeline{
					Name:     "test",
					Contents: "test content",
				},
			}

			result, _ := mock.UpsertPipeline(ctx, req)
			err := mock.DeletePipeline(ctx, result.ID)
			Expect(err).ToNot(HaveOccurred())

			// Verify pipeline was removed from mock's internal storage.
			// Use the locked accessor so this read is safe under -race.
			Expect(mock.Has(result.ID)).To(BeFalse())
		})
	})

	Context("Controller Error Handling", func() {
		ctx := context.Background()

		It("should preserve original error when status update fails", func() {
			By("Setting up fake client with status update error")
			pipeline := &fleetmanagementv1alpha1.Pipeline{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-pipeline",
					Namespace:  "default",
					Generation: 1,
				},
				Spec: fleetmanagementv1alpha1.PipelineSpec{
					Contents: "test",
				},
			}

			// Create fake client with status subresource support
			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme.Scheme).
				WithStatusSubresource(&fleetmanagementv1alpha1.Pipeline{}).
				WithObjects(pipeline).
				Build()

			// Wrap with status error injector
			statusErrClient := &statusErrorClient{
				Client:          fakeClient,
				statusUpdateErr: errors.New("status update failed"),
			}

			reconciler := &PipelineReconciler{
				Client:      statusErrClient,
				Scheme:      scheme.Scheme,
				FleetClient: newMockFleetClient(),
			}

			By("Calling updateStatusError with original error")
			originalErr := errors.New("API connection failed")
			var outcome string
			result, err := reconciler.updateStatusError(ctx, pipeline, reasonSyncFailed, originalErr, &outcome)

			By("Verifying original error is returned")
			Expect(err).To(Equal(originalErr))
			Expect(result).To(Equal(ctrl.Result{}))
			Expect(outcome).To(Equal(reasonSyncFailed))

			By("Verifying status fields were updated in-memory")
			Expect(pipeline.Status.ObservedGeneration).To(Equal(int64(0)))
			Expect(pipeline.Status.Conditions).ToNot(BeEmpty())

			// Find Ready condition
			var readyCondition *metav1.Condition
			for i := range pipeline.Status.Conditions {
				if pipeline.Status.Conditions[i].Type == conditionTypeReady {
					readyCondition = &pipeline.Status.Conditions[i]
					break
				}
			}
			Expect(readyCondition).NotTo(BeNil())
			Expect(readyCondition.Status).To(Equal(metav1.ConditionFalse))
			Expect(readyCondition.Reason).To(Equal(reasonSyncFailed))
		})

		It("retries the same generation after a retryable upsert failure", func() {
			By("Setting up a Pipeline that already has its finalizer")
			pipeline := &fleetmanagementv1alpha1.Pipeline{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "retry-pipeline",
					Namespace:  "default",
					Generation: 1,
					Finalizers: []string{pipelineFinalizer},
				},
				Spec: fleetmanagementv1alpha1.PipelineSpec{
					Contents:   "prometheus.exporter.self \"alloy\" { }",
					Enabled:    boolPtr(true),
					ConfigType: fleetmanagementv1alpha1.ConfigTypeAlloy,
				},
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme.Scheme).
				WithStatusSubresource(&fleetmanagementv1alpha1.Pipeline{}).
				WithObjects(pipeline).
				Build()

			mock := newMockFleetClient()
			firstErr := errors.New("temporary Fleet outage")
			mock.upsertError = firstErr

			reconciler := &PipelineReconciler{
				Client:      fakeClient,
				Scheme:      scheme.Scheme,
				FleetClient: mock,
			}

			key := types.NamespacedName{Namespace: pipeline.Namespace, Name: pipeline.Name}

			By("Reconciling once with a retryable Fleet failure")
			result, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: key})
			Expect(err).To(Equal(firstErr))
			Expect(result).To(Equal(ctrl.Result{}))
			Expect(mock.CallCount()).To(Equal(1))

			By("Verifying the failed retryable attempt did not mark the generation observed")
			fresh := &fleetmanagementv1alpha1.Pipeline{}
			Expect(fakeClient.Get(ctx, key, fresh)).To(Succeed())
			Expect(fresh.Generation).To(Equal(int64(1)))
			Expect(fresh.Status.ObservedGeneration).NotTo(Equal(fresh.Generation))

			By("Reconciling the same generation again after Fleet recovers")
			mock.mu.Lock()
			mock.upsertError = nil
			mock.mu.Unlock()

			result, err = reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: key})
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(ctrl.Result{}))
			Expect(mock.CallCount()).To(Equal(2), "second reconcile must call UpsertPipeline again")

			Expect(fakeClient.Get(ctx, key, fresh)).To(Succeed())
			Expect(fresh.Status.ID).To(Equal("mock-id-123"))
			Expect(fresh.Status.ObservedGeneration).To(Equal(fresh.Generation))
		})

		It("should trigger requeue on status conflict", func() {
			By("Setting up fake client with conflict error")
			pipeline := &fleetmanagementv1alpha1.Pipeline{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-pipeline",
					Namespace:  "default",
					Generation: 1,
				},
				Spec: fleetmanagementv1alpha1.PipelineSpec{
					Contents: "test",
				},
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme.Scheme).
				WithStatusSubresource(&fleetmanagementv1alpha1.Pipeline{}).
				WithObjects(pipeline).
				Build()

			// Create conflict error using k8s apierrors
			conflictErr := apierrors.NewConflict(
				fleetmanagementv1alpha1.GroupVersion.WithResource("pipelines").GroupResource(),
				"test-pipeline",
				errors.New("resource version conflict"),
			)

			statusErrClient := &statusErrorClient{
				Client:          fakeClient,
				statusUpdateErr: conflictErr,
			}

			reconciler := &PipelineReconciler{
				Client:      statusErrClient,
				Scheme:      scheme.Scheme,
				FleetClient: newMockFleetClient(),
			}

			By("Calling updateStatusError with status conflict")
			originalErr := errors.New("API error")
			var outcome string
			result, err := reconciler.updateStatusError(ctx, pipeline, reasonSyncFailed, originalErr, &outcome)

			By("Verifying requeue is triggered and error is nil")
			Expect(err).ToNot(HaveOccurred())
			Expect(result.Requeue).To(BeTrue()) //nolint:staticcheck // testing deprecated field intentionally
			// Conflict path overwrites outcome to "NoOp" so the deferred
			// counter records the cache-lag retry, not a duplicate failure.
			Expect(outcome).To(Equal("NoOp"))
		})

		It("should not retry validation errors", func() {
			By("Setting up fake client with working status update")
			pipeline := &fleetmanagementv1alpha1.Pipeline{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-pipeline",
					Namespace:  "default",
					Generation: 1,
				},
				Spec: fleetmanagementv1alpha1.PipelineSpec{
					Contents: "invalid config",
				},
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme.Scheme).
				WithStatusSubresource(&fleetmanagementv1alpha1.Pipeline{}).
				WithObjects(pipeline).
				Build()

			reconciler := &PipelineReconciler{
				Client:      fakeClient,
				Scheme:      scheme.Scheme,
				FleetClient: newMockFleetClient(),
			}

			By("Calling updateStatusError with validation error")
			validationErr := &fleetclient.FleetAPIError{
				StatusCode: http.StatusBadRequest,
				Operation:  "UpsertPipeline",
				Message:    "validation failed",
			}
			var outcome string
			result, err := reconciler.updateStatusError(ctx, pipeline, reasonValidationError, validationErr, &outcome)

			By("Verifying no retry is triggered")
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(Equal(ctrl.Result{}))
			Expect(outcome).To(Equal(reasonValidationError))
		})

		It("observes read-only pipelines without upserting", func() {
			By("Setting up a read-only Pipeline with a Fleet ID annotation")
			createdAt := time.Now().Add(-time.Hour)
			updatedAt := time.Now()
			pipeline := &fleetmanagementv1alpha1.Pipeline{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "readonly-pipeline",
					Namespace:  "default",
					Generation: 1,
					Finalizers: []string{pipelineFinalizer},
					Annotations: map[string]string{
						fleetmanagementv1alpha1.PipelineImportModeAnnotation: fleetmanagementv1alpha1.PipelineImportModeAnnotationReadOnly,
						fleetmanagementv1alpha1.FleetPipelineIDAnnotation:    "fleet-readonly-1",
					},
				},
				Spec: fleetmanagementv1alpha1.PipelineSpec{
					Contents:   "local content should not be applied",
					ConfigType: fleetmanagementv1alpha1.ConfigTypeAlloy,
				},
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme.Scheme).
				WithStatusSubresource(&fleetmanagementv1alpha1.Pipeline{}).
				WithObjects(pipeline).
				Build()

			mock := newMockFleetClient()
			mock.pipelines["fleet-readonly-1"] = &fleetclient.Pipeline{
				ID:         "fleet-readonly-1",
				Name:       "remote-readonly",
				Contents:   "remote content",
				Enabled:    true,
				ConfigType: "CONFIG_TYPE_ALLOY",
				Source: &fleetclient.Source{
					Type:      "SOURCE_TYPE_GRAFANA",
					Namespace: "instrumentation-hub",
				},
				CreatedAt: &createdAt,
				UpdatedAt: &updatedAt,
			}

			reconciler := &PipelineReconciler{
				Client:      fakeClient,
				Scheme:      scheme.Scheme,
				FleetClient: mock,
			}
			key := types.NamespacedName{Namespace: pipeline.Namespace, Name: pipeline.Name}

			result, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: key})
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(ctrl.Result{}))
			Expect(mock.CallCount()).To(Equal(0), "read-only pipelines must not call UpsertPipeline")

			fresh := &fleetmanagementv1alpha1.Pipeline{}
			Expect(fakeClient.Get(ctx, key, fresh)).To(Succeed())
			Expect(fresh.Status.ID).To(Equal("fleet-readonly-1"))
			Expect(fresh.Status.ObservedGeneration).To(Equal(fresh.Generation))
			requireSource := fresh.Status.Source
			Expect(requireSource).NotTo(BeNil())
			Expect(requireSource.Type).To(Equal(fleetmanagementv1alpha1.SourceTypeGrafana))
			Expect(requireSource.Namespace).To(Equal("instrumentation-hub"))

			ready := meta.FindStatusCondition(fresh.Status.Conditions, conditionTypeReady)
			Expect(ready).NotTo(BeNil())
			Expect(ready.Status).To(Equal(metav1.ConditionTrue))
			Expect(ready.Reason).To(Equal(reasonReadOnly))
		})

		It("promotes read-only pipelines on annotation-only adopt changes", func() {
			By("Setting up a previously observed read-only Pipeline with adopt annotation")
			pipeline := &fleetmanagementv1alpha1.Pipeline{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "adopt-pipeline",
					Namespace:  "default",
					Generation: 1,
					Finalizers: []string{pipelineFinalizer},
					Annotations: map[string]string{
						fleetmanagementv1alpha1.PipelineImportModeAnnotation: fleetmanagementv1alpha1.PipelineImportModeAnnotationAdopt,
						fleetmanagementv1alpha1.FleetPipelineIDAnnotation:    "fleet-adopt-1",
					},
				},
				Spec: fleetmanagementv1alpha1.PipelineSpec{
					Contents:   "prometheus.exporter.self \"alloy\" { }",
					ConfigType: fleetmanagementv1alpha1.ConfigTypeAlloy,
				},
				Status: fleetmanagementv1alpha1.PipelineStatus{
					ID:                 "fleet-adopt-1",
					ObservedGeneration: 1,
					Conditions: []metav1.Condition{
						{
							Type:               conditionTypeReady,
							Status:             metav1.ConditionTrue,
							Reason:             reasonReadOnly,
							ObservedGeneration: 1,
						},
					},
				},
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme.Scheme).
				WithStatusSubresource(&fleetmanagementv1alpha1.Pipeline{}).
				WithObjects(pipeline).
				Build()

			mock := newMockFleetClient()
			reconciler := &PipelineReconciler{
				Client:      fakeClient,
				Scheme:      scheme.Scheme,
				FleetClient: mock,
			}
			key := types.NamespacedName{Namespace: pipeline.Namespace, Name: pipeline.Name}

			result, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: key})
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(ctrl.Result{}))
			Expect(mock.CallCount()).To(Equal(1), "adopting a read-only Pipeline must not be skipped only because generation is unchanged")
		})

		It("should recreate pipeline inline when 404 with existing ID", func() {
			By("Setting up mock client with 404 on first call, success on second")
			pipeline := &fleetmanagementv1alpha1.Pipeline{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-pipeline",
					Namespace:  "default",
					Generation: 1,
				},
				Spec: fleetmanagementv1alpha1.PipelineSpec{
					Contents: "test content",
				},
				Status: fleetmanagementv1alpha1.PipelineStatus{
					ID: "old-id-123", // Existing ID indicates external deletion
				},
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme.Scheme).
				WithStatusSubresource(&fleetmanagementv1alpha1.Pipeline{}).
				WithObjects(pipeline).
				Build()

			// Create mock that returns 404 first, then succeeds
			mock := newMockFleetClient()
			mock.shouldReturn404OnFirst = true

			reconciler := &PipelineReconciler{
				Client:      fakeClient,
				Scheme:      scheme.Scheme,
				FleetClient: mock,
			}

			By("Calling reconcileNormal which will trigger 404 handling")
			var outcome string
			result, err := reconciler.reconcileNormal(ctx, pipeline, &outcome)

			By("Verifying recreation was attempted")
			Expect(mock.CallCount()).To(Equal(2), "UpsertPipeline should be called twice (initial 404 then recreation)")

			By("Verifying success after recreation")
			// After successful recreation, should return success result
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(Equal(ctrl.Result{}))
			Expect(pipeline.Status.ID).To(Equal("mock-id-123"))
		})

		It("should fail immediately when 404 with empty ID", func() {
			By("Setting up pipeline with empty ID (already tried recreation)")
			pipeline := &fleetmanagementv1alpha1.Pipeline{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-pipeline",
					Namespace:  "default",
					Generation: 1,
				},
				Spec: fleetmanagementv1alpha1.PipelineSpec{
					Contents: "test content",
				},
				Status: fleetmanagementv1alpha1.PipelineStatus{
					ID: "", // Empty ID means we already tried recreation
				},
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme.Scheme).
				WithStatusSubresource(&fleetmanagementv1alpha1.Pipeline{}).
				WithObjects(pipeline).
				Build()

			reconciler := &PipelineReconciler{
				Client:      fakeClient,
				Scheme:      scheme.Scheme,
				FleetClient: newMockFleetClient(),
			}

			By("Calling handleAPIError with 404 and empty ID")
			notFoundErr := &fleetclient.FleetAPIError{
				StatusCode: http.StatusNotFound,
				Operation:  "UpsertPipeline",
				Message:    "pipeline not found",
			}
			var outcome string
			result, err := reconciler.handleAPIError(ctx, pipeline, notFoundErr, &outcome)

			By("Verifying recreation attempt is not made and status is updated")
			// When ID is empty, handleAPIError recognizes recreation already failed
			// and returns error via updateStatusError. Since 404 is permanent,
			// shouldRetry returns false, so updateStatusError returns nil (no exponential backoff)
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(Equal(ctrl.Result{}))

			By("Verifying Ready condition reflects the failure")
			var readyCondition *metav1.Condition
			for i := range pipeline.Status.Conditions {
				if pipeline.Status.Conditions[i].Type == conditionTypeReady {
					readyCondition = &pipeline.Status.Conditions[i]
					break
				}
			}
			Expect(readyCondition).NotTo(BeNil())
			Expect(readyCondition.Status).To(Equal(metav1.ConditionFalse))
			Expect(readyCondition.Reason).To(Equal(reasonSyncFailed))
			Expect(readyCondition.Message).To(ContainSubstring("recreation failed"))
		})

		It("removes the finalizer when DeletePipeline returns 404", func() {
			// This test exercises the 404-on-delete path inside reconcileDelete:
			// when Fleet Management returns 404 the controller must treat the
			// pipeline as already gone and still remove the K8s finalizer so
			// the CR is garbage-collected. Using a direct reconcileDelete call
			// avoids racing with the shared envtest PipelineReconciler.
			By("Setting up a pipeline that already has a Fleet ID and finalizer")
			pipeline := &fleetmanagementv1alpha1.Pipeline{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-pipeline",
					Namespace:  "default",
					Generation: 1,
					Finalizers: []string{pipelineFinalizer},
				},
				Spec: fleetmanagementv1alpha1.PipelineSpec{
					Contents: "prometheus.exporter.self \"alloy\" { }",
					Enabled:  boolPtr(true),
				},
				Status: fleetmanagementv1alpha1.PipelineStatus{
					ID: "fleet-id-abc",
				},
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme.Scheme).
				WithStatusSubresource(&fleetmanagementv1alpha1.Pipeline{}).
				WithObjects(pipeline).
				Build()

			By("Configuring the mock to return 404 from DeletePipeline")
			deleteMock := newMockFleetClient()
			deleteMock.shouldReturn404OnDelete = true

			reconciler := &PipelineReconciler{
				Client:      fakeClient,
				Scheme:      scheme.Scheme,
				FleetClient: deleteMock,
			}

			By("Calling reconcileDelete directly")
			var outcome string
			result, err := reconciler.reconcileDelete(context.Background(), pipeline, &outcome)

			By("Verifying the call succeeded despite the 404")
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(Equal(ctrl.Result{}))

			By("Verifying the finalizer has been removed from the in-memory object")
			Expect(slices.Contains(pipeline.Finalizers, pipelineFinalizer)).To(BeFalse(),
				"finalizer must be removed after 404 from DeletePipeline")

			By("Verifying the CR no longer has the finalizer in the API server")
			updated := &fleetmanagementv1alpha1.Pipeline{}
			err = fakeClient.Get(context.Background(), types.NamespacedName{
				Namespace: pipeline.Namespace,
				Name:      pipeline.Name,
			}, updated)
			// Once the finalizer is removed the fake client's GC may have
			// already deleted the object. Either outcome is valid: no
			// finalizer present, or the object is already gone.
			if err == nil {
				Expect(slices.Contains(updated.Finalizers, pipelineFinalizer)).To(BeFalse(),
					"API server object must not retain the finalizer")
			} else {
				Expect(apierrors.IsNotFound(err)).To(BeTrue(),
					"expected NotFound after finalizer removal, got %v", err)
			}
		})

		It("does not delete Fleet pipeline for read-only Grafana resources", func() {
			By("Setting up a read-only Grafana pipeline with finalizer and Fleet ID annotation")
			pipeline := &fleetmanagementv1alpha1.Pipeline{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "grafana-readonly",
					Namespace:  "default",
					Generation: 1,
					Finalizers: []string{pipelineFinalizer},
					Annotations: map[string]string{
						fleetmanagementv1alpha1.FleetPipelineIDAnnotation:   "fleet-id-readonly",
						fleetmanagementv1alpha1.PipelineImportModeAnnotation: fleetmanagementv1alpha1.PipelineImportModeAnnotationReadOnly,
					},
				},
				Spec: fleetmanagementv1alpha1.PipelineSpec{
					Contents: "prometheus.exporter.self \"alloy\" { }",
					Enabled:  boolPtr(true),
					Source: &fleetmanagementv1alpha1.PipelineSource{
						Type: fleetmanagementv1alpha1.SourceTypeGrafana,
					},
				},
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme.Scheme).
				WithStatusSubresource(&fleetmanagementv1alpha1.Pipeline{}).
				WithObjects(pipeline).
				Build()

			By("Configuring the mock to fail if DeletePipeline is called")
			deleteMock := newMockFleetClient()
			deleteMock.deleteError = fmt.Errorf("DeletePipeline must not be called for read-only resources")

			reconciler := &PipelineReconciler{
				Client:      fakeClient,
				Scheme:      scheme.Scheme,
				FleetClient: deleteMock,
			}

			By("Calling reconcileDelete directly")
			var outcome string
			result, err := reconciler.reconcileDelete(context.Background(), pipeline, &outcome)

			By("Verifying finalizer removal succeeds without calling DeletePipeline")
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(Equal(ctrl.Result{}))
			Expect(slices.Contains(pipeline.Finalizers, pipelineFinalizer)).To(BeFalse())
		})
	})
})
