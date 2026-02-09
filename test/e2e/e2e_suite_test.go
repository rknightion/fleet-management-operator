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
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/grafana/fleet-management-operator/test/utils"
)

// namespace where the project is deployed in
const namespace = "fm-crd-system"

var (
	// managerImage is the manager image to be built and loaded for testing.
	managerImage = "example.com/fm-crd:v1.0.0"
	// mockAPIImage is the mock Fleet Management API image for e2e tests.
	mockAPIImage = "mock-fleet-api:test"
	// shouldCleanupCertManager tracks whether CertManager was installed by this suite.
	shouldCleanupCertManager = false
)

// TestE2E runs the e2e test suite to validate the solution in an isolated environment.
// The default setup requires Kind and CertManager.
//
// To skip CertManager installation, set: CERT_MANAGER_INSTALL_SKIP=true
func TestE2E(t *testing.T) {
	RegisterFailHandler(Fail)
	_, _ = fmt.Fprintf(GinkgoWriter, "Starting fm-crd e2e test suite\n")
	RunSpecs(t, "e2e suite")
}

var _ = BeforeSuite(func() {
	By("building the manager image")
	cmd := exec.Command("make", "docker-build-load", fmt.Sprintf("IMG=%s", managerImage))
	_, err := utils.Run(cmd)
	ExpectWithOffset(1, err).NotTo(HaveOccurred(), "Failed to build the manager image")

	// TODO(user): If you want to change the e2e test vendor from Kind,
	// ensure the image is built and available, then remove the following block.
	By("loading the manager image on Kind")
	err = utils.LoadImageToKindClusterWithName(managerImage)
	ExpectWithOffset(1, err).NotTo(HaveOccurred(), "Failed to load the manager image into Kind")

	By("building the mock Fleet API image")
	cmd = exec.Command(os.Getenv("CONTAINER_TOOL"), "build", "-t", mockAPIImage, "test/mockapi/")
	if cmd.Args[0] == "" {
		cmd = exec.Command("docker", "build", "-t", mockAPIImage, "test/mockapi/")
	}
	_, err = utils.Run(cmd)
	ExpectWithOffset(1, err).NotTo(HaveOccurred(), "Failed to build the mock Fleet API image")

	By("loading the mock Fleet API image on Kind")
	err = utils.LoadImageToKindClusterWithName(mockAPIImage)
	ExpectWithOffset(1, err).NotTo(HaveOccurred(), "Failed to load the mock Fleet API image into Kind")

	setupCertManager()

	By("creating manager namespace")
	cmd = exec.Command("kubectl", "create", "ns", namespace)
	_, err = utils.Run(cmd)
	ExpectWithOffset(1, err).NotTo(HaveOccurred(), "Failed to create namespace")

	By("labeling the namespace to enforce the restricted security policy")
	cmd = exec.Command("kubectl", "label", "--overwrite", "ns", namespace,
		"pod-security.kubernetes.io/enforce=restricted")
	_, err = utils.Run(cmd)
	ExpectWithOffset(1, err).NotTo(HaveOccurred(), "Failed to label namespace with restricted policy")

	By("installing CRDs")
	cmd = exec.Command("make", "install")
	_, err = utils.Run(cmd)
	ExpectWithOffset(1, err).NotTo(HaveOccurred(), "Failed to install CRDs")

	By("deploying the mock Fleet API")
	cmd = exec.Command("kubectl", "apply", "-f", "test/mockapi/manifests/", "-n", namespace)
	_, err = utils.Run(cmd)
	ExpectWithOffset(1, err).NotTo(HaveOccurred(), "Failed to deploy mock Fleet API")

	By("waiting for mock Fleet API to be ready")
	Eventually(func(g Gomega) {
		cmd := exec.Command("kubectl", "get", "pods", "-l", "app=mock-fleet-api",
			"-o", "jsonpath={.items[0].status.phase}", "-n", namespace)
		output, err := utils.Run(cmd)
		g.Expect(err).NotTo(HaveOccurred(), "Failed to get mock Fleet API pod status")
		g.Expect(output).To(Equal("Running"), "Mock Fleet API pod not running")

		cmd = exec.Command("kubectl", "get", "pods", "-l", "app=mock-fleet-api",
			"-o", "jsonpath={.items[0].status.conditions[?(@.type=='Ready')].status}", "-n", namespace)
		output, err = utils.Run(cmd)
		g.Expect(err).NotTo(HaveOccurred(), "Failed to get mock Fleet API pod readiness")
		g.Expect(output).To(Equal("True"), "Mock Fleet API pod not ready")
	}).WithTimeout(2 * time.Minute).WithPolling(5 * time.Second).Should(Succeed())

	By("deploying the controller-manager with E2E kustomization")
	// Use E2E-specific kustomization that deploys to fm-crd-system namespace with webhooks enabled
	cmd = exec.Command("sh", "-c", "bin/kustomize build config/e2e | kubectl apply -f -")
	_, err = utils.Run(cmd)
	ExpectWithOffset(1, err).NotTo(HaveOccurred(), "Failed to deploy the controller-manager")

	By("waiting for webhook service to be ready")
	Eventually(func(g Gomega) {
		cmd := exec.Command("kubectl", "get", "service", "fm-crd-webhook-service", "-n", namespace)
		_, err := utils.Run(cmd)
		g.Expect(err).NotTo(HaveOccurred(), "Webhook service should exist")
	}).WithTimeout(2 * time.Minute).WithPolling(5 * time.Second).Should(Succeed())

	By("waiting for webhook endpoint to be ready")
	Eventually(func(g Gomega) {
		cmd := exec.Command("kubectl", "get", "endpoints", "fm-crd-webhook-service", "-n", namespace,
			"-o", "jsonpath={.subsets[0].addresses[0].ip}")
		output, err := utils.Run(cmd)
		g.Expect(err).NotTo(HaveOccurred(), "Failed to get webhook endpoint")
		g.Expect(output).NotTo(BeEmpty(), "Webhook endpoint should have an IP address")
	}).WithTimeout(2 * time.Minute).WithPolling(5 * time.Second).Should(Succeed())
})

var _ = AfterSuite(func() {
	By("cleaning up the curl pod for metrics")
	cmd := exec.Command("kubectl", "delete", "pod", "curl-metrics", "-n", namespace, "--ignore-not-found")
	_, _ = utils.Run(cmd)

	By("undeploying the controller-manager")
	cmd = exec.Command("sh", "-c", "bin/kustomize build config/e2e | kubectl delete -f - --ignore-not-found")
	_, _ = utils.Run(cmd)

	By("undeploying the mock Fleet API")
	cmd = exec.Command("kubectl", "delete", "-f", "test/mockapi/manifests/", "-n", namespace, "--ignore-not-found")
	_, _ = utils.Run(cmd)

	By("uninstalling CRDs")
	cmd = exec.Command("make", "uninstall")
	_, _ = utils.Run(cmd)

	By("removing manager namespace")
	cmd = exec.Command("kubectl", "delete", "ns", namespace, "--ignore-not-found")
	_, _ = utils.Run(cmd)

	teardownCertManager()
})

// setupCertManager installs CertManager if needed for webhook tests.
// Skips installation if CERT_MANAGER_INSTALL_SKIP=true or if already present.
func setupCertManager() {
	if os.Getenv("CERT_MANAGER_INSTALL_SKIP") == "true" {
		_, _ = fmt.Fprintf(GinkgoWriter, "Skipping CertManager installation (CERT_MANAGER_INSTALL_SKIP=true)\n")
		return
	}

	By("checking if CertManager is already installed")
	if utils.IsCertManagerCRDsInstalled() {
		_, _ = fmt.Fprintf(GinkgoWriter, "CertManager is already installed. Skipping installation.\n")
		return
	}

	// Mark for cleanup before installation to handle interruptions and partial installs.
	shouldCleanupCertManager = true

	By("installing CertManager")
	Expect(utils.InstallCertManager()).To(Succeed(), "Failed to install CertManager")
}

// teardownCertManager uninstalls CertManager if it was installed by setupCertManager.
// This ensures we only remove what we installed.
func teardownCertManager() {
	if !shouldCleanupCertManager {
		_, _ = fmt.Fprintf(GinkgoWriter, "Skipping CertManager cleanup (not installed by this suite)\n")
		return
	}

	By("uninstalling CertManager")
	utils.UninstallCertManager()
}
