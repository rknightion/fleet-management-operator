//go:build e2e
// +build e2e

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

package e2e

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/grafana/fleet-management-operator/test/utils"
)

var _ = Describe("Pipeline Lifecycle", Ordered, func() {
	Context("Pipeline Lifecycle", func() {
		It("should create an Alloy pipeline and reach Ready state", func() {
			By("applying the valid Alloy pipeline fixture")
			cmd := exec.Command("kubectl", "apply", "-f", "test/e2e/fixtures/valid-alloy-pipeline.yaml", "-n", namespace)
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to apply Alloy pipeline fixture")

			By("waiting for the pipeline to have a Fleet Management ID")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "pipeline", "test-alloy-pipeline", "-n", namespace,
					"-o", "jsonpath={.status.id}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred(), "Failed to get pipeline status.id")
				g.Expect(output).NotTo(BeEmpty(), "Pipeline status.id should be assigned")
			}).WithTimeout(2 * time.Minute).WithPolling(5 * time.Second).Should(Succeed())

			By("verifying the Ready condition is True")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "pipeline", "test-alloy-pipeline", "-n", namespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred(), "Failed to get Ready condition status")
				g.Expect(output).To(Equal("True"), "Pipeline Ready condition should be True")
			}).WithTimeout(2 * time.Minute).WithPolling(5 * time.Second).Should(Succeed())

			By("verifying observedGeneration matches generation")
			cmd = exec.Command("kubectl", "get", "pipeline", "test-alloy-pipeline", "-n", namespace,
				"-o", "jsonpath={.metadata.generation}")
			generationOutput, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to get pipeline generation")

			cmd = exec.Command("kubectl", "get", "pipeline", "test-alloy-pipeline", "-n", namespace,
				"-o", "jsonpath={.status.observedGeneration}")
			observedGenOutput, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to get pipeline observedGeneration")

			generation, err := strconv.ParseInt(strings.TrimSpace(generationOutput), 10, 64)
			Expect(err).NotTo(HaveOccurred(), "Failed to parse generation")
			Expect(generation).To(BeNumerically(">", 0), "Generation should be positive")

			observedGen, err := strconv.ParseInt(strings.TrimSpace(observedGenOutput), 10, 64)
			Expect(err).NotTo(HaveOccurred(), "Failed to parse observedGeneration")
			Expect(observedGen).To(Equal(generation), "ObservedGeneration should match generation")
		})

		It("should create an OpenTelemetry Collector pipeline and reach Ready state", func() {
			By("applying the valid OTEL pipeline fixture")
			cmd := exec.Command("kubectl", "apply", "-f", "test/e2e/fixtures/valid-otel-pipeline.yaml", "-n", namespace)
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to apply OTEL pipeline fixture")

			By("waiting for the pipeline to reach Ready state")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "pipeline", "test-otel-pipeline", "-n", namespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred(), "Failed to get Ready condition status")
				g.Expect(output).To(Equal("True"), "Pipeline Ready condition should be True")
			}).WithTimeout(2 * time.Minute).WithPolling(5 * time.Second).Should(Succeed())

			By("verifying the Fleet Management ID is assigned")
			cmd = exec.Command("kubectl", "get", "pipeline", "test-otel-pipeline", "-n", namespace,
				"-o", "jsonpath={.status.id}")
			output, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to get pipeline status.id")
			Expect(output).NotTo(BeEmpty(), "Pipeline status.id should be assigned")
		})

		It("should update a pipeline and reconcile the new spec", func() {
			By("getting the current generation of the Alloy pipeline")
			cmd := exec.Command("kubectl", "get", "pipeline", "test-alloy-pipeline", "-n", namespace,
				"-o", "jsonpath={.metadata.generation}")
			beforeGenOutput, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to get pipeline generation")
			beforeGen, err := strconv.ParseInt(strings.TrimSpace(beforeGenOutput), 10, 64)
			Expect(err).NotTo(HaveOccurred(), "Failed to parse generation")

			By("applying the updated Alloy pipeline fixture")
			cmd = exec.Command("kubectl", "apply", "-f", "test/e2e/fixtures/update-alloy-pipeline.yaml", "-n", namespace)
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to apply updated pipeline fixture")

			By("waiting for observedGeneration to advance")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "pipeline", "test-alloy-pipeline", "-n", namespace,
					"-o", "jsonpath={.status.observedGeneration}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred(), "Failed to get observedGeneration")
				observedGen, err := strconv.ParseInt(strings.TrimSpace(output), 10, 64)
				g.Expect(err).NotTo(HaveOccurred(), "Failed to parse observedGeneration")
				g.Expect(observedGen).To(BeNumerically(">=", beforeGen+1), "ObservedGeneration should advance after update")
			}).WithTimeout(2 * time.Minute).WithPolling(5 * time.Second).Should(Succeed())

			By("verifying the pipeline remains Ready after update")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "pipeline", "test-alloy-pipeline", "-n", namespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred(), "Failed to get Ready condition status")
				g.Expect(output).To(Equal("True"), "Pipeline Ready condition should remain True")
			}).WithTimeout(2 * time.Minute).WithPolling(5 * time.Second).Should(Succeed())
		})

		It("should reject an invalid pipeline via webhook", func() {
			if os.Getenv("CERT_MANAGER_INSTALL_SKIP") == "true" {
				Skip("Skipping webhook test - CertManager is not installed")
			}

			By("attempting to apply a pipeline with mismatched configType")
			cmd := exec.Command("kubectl", "apply", "-f",
				"test/e2e/fixtures/invalid-mismatch-pipeline.yaml", "-n", namespace)
			output, err := utils.Run(cmd)

			// Webhook should reject it - expect an error
			Expect(err).To(HaveOccurred(), "Webhook should reject mismatched configType")
			Expect(output).To(ContainSubstring("denied"), "Error should indicate webhook denial")
		})

		It("should delete a pipeline and remove the finalizer", func() {
			By("deleting the Alloy pipeline")
			cmd := exec.Command("kubectl", "delete", "pipeline", "test-alloy-pipeline", "-n", namespace)
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to delete Alloy pipeline")

			By("verifying the pipeline is fully deleted")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "pipeline", "test-alloy-pipeline", "-n", namespace)
				output, err := utils.Run(cmd)
				// Should fail with NotFound error
				g.Expect(err).To(HaveOccurred(), "Pipeline should be deleted")
				g.Expect(output).To(ContainSubstring("NotFound"), "Error should indicate pipeline not found")
			}).WithTimeout(2 * time.Minute).WithPolling(5 * time.Second).Should(Succeed())

			By("deleting the OTEL pipeline")
			cmd = exec.Command("kubectl", "delete", "pipeline", "test-otel-pipeline", "-n", namespace)
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to delete OTEL pipeline")

			By("verifying the OTEL pipeline is fully deleted")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "pipeline", "test-otel-pipeline", "-n", namespace)
				output, err := utils.Run(cmd)
				// Should fail with NotFound error
				g.Expect(err).To(HaveOccurred(), "Pipeline should be deleted")
				g.Expect(output).To(ContainSubstring("NotFound"), "Error should indicate pipeline not found")
			}).WithTimeout(2 * time.Minute).WithPolling(5 * time.Second).Should(Succeed())
		})

		// Add cleanup in case tests fail partway through
		AfterAll(func() {
			By("cleaning up test pipelines if they still exist")
			cmd := exec.Command("kubectl", "delete", "pipeline", "test-alloy-pipeline", "-n", namespace, "--ignore-not-found")
			_, _ = utils.Run(cmd)

			cmd = exec.Command("kubectl", "delete", "pipeline", "test-otel-pipeline", "-n", namespace, "--ignore-not-found")
			_, _ = utils.Run(cmd)
		})
	})
})
