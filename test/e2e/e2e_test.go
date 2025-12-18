/*
Copyright 2025 ContainerInfra.

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
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/containerinfra/kube-deployment-coordinator/test/utils"
)

// namespace where the project is deployed in
const namespace = "kube-deployment-coordinator-system"

// serviceAccountName created for the project
const serviceAccountName = "kube-deployment-coordinator-controller-manager"

// metricsServiceName is the name of the metrics service of the project
const metricsServiceName = "kube-deployment-coordinator-controller-manager-metrics-service"

// metricsRoleBindingName is the name of the RBAC that will be created to allow get the metrics data
const metricsRoleBindingName = "kube-deployment-coordinator-metrics-binding"

var _ = Describe("Manager", Ordered, func() {
	var controllerPodName string

	// Before running the tests, set up the environment by creating the namespace,
	// installing CRDs, and deploying the controller.
	BeforeAll(func() {
		By("creating manager namespace")
		cmd := exec.Command("kubectl", "create", "ns", namespace)
		_, err := utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to create namespace")

		By("installing CRDs")
		cmd = exec.Command("make", "install")
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to install CRDs")

		By("waiting for cert-manager webhook to be fully ready")
		// Wait for cert-manager webhook to be ready and accepting requests
		// This is necessary because the Certificate resource creation will fail
		// if cert-manager's webhook isn't ready yet (TLS certificate issues)
		// The webhook needs time to bootstrap its own TLS certificates
		verifyCertManagerWebhookReady := func(g Gomega) {
			// Check that cert-manager-webhook deployment exists and is available
			cmd := exec.Command("kubectl", "get", "deployment", "cert-manager-webhook",
				"-n", "cert-manager",
				"-o", "jsonpath={.status.conditions[?(@.type==\"Available\")].status}")
			output, err := utils.Run(cmd)
			// If cert-manager isn't installed, skip the check
			if err != nil {
				return
			}
			g.Expect(output).To(Equal("True"), "cert-manager-webhook should be available")

			// Check that webhook pods are ready
			cmd = exec.Command("kubectl", "get", "pods", "-n", "cert-manager",
				"-l", "app.kubernetes.io/name=cert-manager-webhook",
				"-o", "jsonpath={.items[*].status.conditions[?(@.type==\"Ready\")].status}")
			output, err = utils.Run(cmd)
			if err == nil && output != "" {
				// All pods should be ready
				readyCount := strings.Count(output, "True")
				g.Expect(readyCount).To(BeNumerically(">", 0), "At least one cert-manager-webhook pod should be ready")
			}
		}
		Eventually(verifyCertManagerWebhookReady, 3*time.Minute, 5*time.Second).Should(Succeed())

		// Additional wait to ensure webhook TLS certificates are fully ready
		// Cert-manager webhook needs time to bootstrap its own certificates
		time.Sleep(10 * time.Second)

		By("deploying the controller-manager")
		// Retry deployment in case cert-manager webhook isn't ready yet
		deployController := func() error {
			cmd = exec.Command("make", "deploy", fmt.Sprintf("IMG=%s", projectImage))
			_, err = utils.Run(cmd)
			return err
		}
		Eventually(deployController, 3*time.Minute, 10*time.Second).Should(Succeed(), "Failed to deploy the controller-manager")
	})

	// After all tests have been executed, clean up by undeploying the controller, uninstalling CRDs,
	// and deleting the namespace.
	AfterAll(func() {
		By("cleaning up the curl pod for metrics")
		cmd := exec.Command("kubectl", "delete", "pod", "curl-metrics", "-n", namespace)
		_, _ = utils.Run(cmd)

		By("undeploying the controller-manager")
		cmd = exec.Command("make", "undeploy")
		_, _ = utils.Run(cmd)

		By("uninstalling CRDs")
		cmd = exec.Command("make", "uninstall")
		_, _ = utils.Run(cmd)

		By("removing manager namespace")
		cmd = exec.Command("kubectl", "delete", "ns", namespace)
		_, _ = utils.Run(cmd)
	})

	// After each test, check for failures and collect logs, events,
	// and pod descriptions for debugging.
	AfterEach(func() {
		specReport := CurrentSpecReport()
		if specReport.Failed() {
			By("Fetching controller manager pod logs")
			cmd := exec.Command("kubectl", "logs", controllerPodName, "-n", namespace)
			controllerLogs, err := utils.Run(cmd)
			if err == nil {
				_, _ = fmt.Fprintf(GinkgoWriter, "Controller logs:\n %s", controllerLogs)
			} else {
				_, _ = fmt.Fprintf(GinkgoWriter, "Failed to get Controller logs: %s", err)
			}

			By("Fetching Kubernetes events")
			cmd = exec.Command("kubectl", "get", "events", "-n", namespace, "--sort-by=.lastTimestamp")
			eventsOutput, err := utils.Run(cmd)
			if err == nil {
				_, _ = fmt.Fprintf(GinkgoWriter, "Kubernetes events:\n%s", eventsOutput)
			} else {
				_, _ = fmt.Fprintf(GinkgoWriter, "Failed to get Kubernetes events: %s", err)
			}

			By("Fetching curl-metrics logs")
			cmd = exec.Command("kubectl", "logs", "curl-metrics", "-n", namespace)
			metricsOutput, err := utils.Run(cmd)
			if err == nil {
				_, _ = fmt.Fprintf(GinkgoWriter, "Metrics logs:\n %s", metricsOutput)
			} else {
				_, _ = fmt.Fprintf(GinkgoWriter, "Failed to get curl-metrics logs: %s", err)
			}

			By("Fetching controller manager pod description")
			cmd = exec.Command("kubectl", "describe", "pod", controllerPodName, "-n", namespace)
			podDescription, err := utils.Run(cmd)
			if err == nil {
				fmt.Println("Pod description:\n", podDescription)
			} else {
				fmt.Println("Failed to describe controller pod")
			}
		}
	})

	SetDefaultEventuallyTimeout(2 * time.Minute)
	SetDefaultEventuallyPollingInterval(time.Second)

	Context("Manager", func() {
		It("should run successfully", func() {
			By("validating that the controller-manager pod is running as expected")
			verifyControllerUp := func(g Gomega) {
				// Get the name of the controller-manager pod
				cmd := exec.Command("kubectl", "get",
					"pods", "-l", "control-plane=controller-manager",
					"-o", "go-template={{ range .items }}"+
						"{{ if not .metadata.deletionTimestamp }}"+
						"{{ .metadata.name }}"+
						"{{ \"\\n\" }}{{ end }}{{ end }}",
					"-n", namespace,
				)

				podOutput, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred(), "Failed to retrieve controller-manager pod information")
				podNames := utils.GetNonEmptyLines(podOutput)
				g.Expect(podNames).To(HaveLen(1), "expected 1 controller pod running")
				controllerPodName = podNames[0]
				g.Expect(controllerPodName).To(ContainSubstring("controller-manager"))

				// Validate the pod's status
				cmd = exec.Command("kubectl", "get",
					"pods", controllerPodName, "-o", "jsonpath={.status.phase}",
					"-n", namespace,
				)
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("Running"), "Incorrect controller-manager pod status")
			}
			Eventually(verifyControllerUp).Should(Succeed())
		})

		It("should ensure the metrics endpoint is serving metrics", func() {
			By("creating a ClusterRoleBinding for the service account to allow access to metrics")
			cmd := exec.Command("kubectl", "create", "clusterrolebinding", metricsRoleBindingName,
				"--clusterrole=kube-deployment-coordinator-metrics-reader",
				fmt.Sprintf("--serviceaccount=%s:%s", namespace, serviceAccountName),
			)
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to create ClusterRoleBinding")

			By("validating that the metrics service is available")
			cmd = exec.Command("kubectl", "get", "service", metricsServiceName, "-n", namespace)
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Metrics service should exist")

			By("validating that the ServiceMonitor for Prometheus is applied in the namespace")
			cmd = exec.Command("kubectl", "get", "ServiceMonitor", "-n", namespace)
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "ServiceMonitor should exist")

			By("getting the service account token")
			token, err := serviceAccountToken()
			Expect(err).NotTo(HaveOccurred())
			Expect(token).NotTo(BeEmpty())

			By("waiting for the metrics endpoint to be ready")
			verifyMetricsEndpointReady := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "endpoints", metricsServiceName, "-n", namespace)
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(ContainSubstring("8443"), "Metrics endpoint is not ready")
			}
			Eventually(verifyMetricsEndpointReady).Should(Succeed())

			By("verifying that the controller manager is serving the metrics server")
			verifyMetricsServerStarted := func(g Gomega) {
				cmd := exec.Command("kubectl", "logs", controllerPodName, "-n", namespace)
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(ContainSubstring("controller-runtime.metrics\tServing metrics server"),
					"Metrics server not yet started")
			}
			Eventually(verifyMetricsServerStarted).Should(Succeed())

			By("creating the curl-metrics pod to access the metrics endpoint")
			cmd = exec.Command("kubectl", "run", "curl-metrics", "--restart=Never",
				"--namespace", namespace,
				"--image=curlimages/curl:7.78.0",
				"--", "/bin/sh", "-c", fmt.Sprintf(
					"curl -v -k -H 'Authorization: Bearer %s' https://%s.%s.svc.cluster.local:8443/metrics",
					token, metricsServiceName, namespace))
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to create curl-metrics pod")

			By("waiting for the curl-metrics pod to complete.")
			verifyCurlUp := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "pods", "curl-metrics",
					"-o", "jsonpath={.status.phase}",
					"-n", namespace)
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("Succeeded"), "curl pod in wrong status")
			}
			Eventually(verifyCurlUp, 5*time.Minute).Should(Succeed())

			By("getting the metrics by checking curl-metrics logs")
			metricsOutput := getMetricsOutput()
			Expect(metricsOutput).To(ContainSubstring(
				"controller_runtime_reconcile_total",
			))
		})

		It("should provisioned cert-manager", func() {
			By("validating that cert-manager has the certificate Secret")
			verifyCertManager := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "secrets", "webhook-server-cert", "-n", namespace)
				_, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
			}
			Eventually(verifyCertManager).Should(Succeed())
		})

		// +kubebuilder:scaffold:e2e-webhooks-checks
	})

	Context("DeploymentCoordination", func() {
		const testNamespace = "e2e-coordination-test"
		const coordinationName = "test-coordination"

		BeforeEach(func() {
			By("creating test namespace")
			cmd := exec.Command("kubectl", "create", "ns", testNamespace)
			_, err := utils.Run(cmd)
			// Ignore error if namespace already exists
			_ = err
		})

		AfterEach(func() {
			By("cleaning up test resources")
			cmd := exec.Command("kubectl", "delete", "deployment", "--all", "-n", testNamespace)
			_, _ = utils.Run(cmd)
			cmd = exec.Command("kubectl", "delete", "deploymentcoordination", "--all", "-n", testNamespace)
			_, _ = utils.Run(cmd)
			cmd = exec.Command("kubectl", "delete", "ns", testNamespace)
			_, _ = utils.Run(cmd)
		})

		It("should coordinate multiple deployments to roll out one at a time", func() {
			By("creating a DeploymentCoordination resource")
			coordinationYAML := fmt.Sprintf(`apiVersion: apps.containerinfra.nl/v1
kind: DeploymentCoordination
metadata:
  name: %s
  namespace: %s
spec:
  labelSelector:
    matchLabels:
      coordinated: "true"
  minReadySeconds: 0
`, coordinationName, testNamespace)
			coordinationFile := writeTempFile(coordinationYAML)
			defer os.Remove(coordinationFile)

			cmd := exec.Command("kubectl", "apply", "-f", coordinationFile)
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to create DeploymentCoordination")

			By("creating three deployments that match the label selector")
			for i := 1; i <= 3; i++ {
				deploymentYAML := fmt.Sprintf(`apiVersion: apps/v1
kind: Deployment
metadata:
  name: deployment-%d
  namespace: %s
  labels:
    coordinated: "true"
spec:
  replicas: 1
  selector:
    matchLabels:
      app: deployment-%d
  template:
    metadata:
      labels:
        app: deployment-%d
    spec:
      containers:
      - name: nginx
        image: nginx:1.21
        ports:
        - containerPort: 80
`, i, testNamespace, i, i)
				deploymentFile := writeTempFile(deploymentYAML)
				defer os.Remove(deploymentFile)

				cmd = exec.Command("kubectl", "apply", "-f", deploymentFile)
				_, err = utils.Run(cmd)
				Expect(err).NotTo(HaveOccurred(), "Failed to create deployment-%d", i)
			}

			By("verifying that only one deployment is active at a time")
			verifyCoordination := func(g Gomega) {
				// Get the DeploymentCoordination status
				cmd := exec.Command("kubectl", "get", "deploymentcoordination", coordinationName,
					"-n", testNamespace, "-o", "jsonpath={.status.activeDeployment}")
				activeDeployment, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())

				// Get all deployments and their paused status
				cmd = exec.Command("kubectl", "get", "deployment", "-n", testNamespace,
					"-o", "jsonpath={range .items[*]}{.metadata.name}{' '}{.spec.paused}{'\\n'}{end}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())

				lines := utils.GetNonEmptyLines(output)
				g.Expect(lines).To(HaveLen(3), "Expected 3 deployments")

				unpausedCount := 0
				unpausedDeployments := []string{}
				for _, line := range lines {
					// Line format: "deployment-name true" or "deployment-name false"
					// Split by space - last part is the paused status
					parts := strings.Fields(line)
					if len(parts) >= 2 {
						name := parts[0]
						paused := parts[len(parts)-1] // Get last field in case name has spaces
						if paused == "false" {
							unpausedCount++
							unpausedDeployments = append(unpausedDeployments, name)
						}
					}
				}

				// If there's an active deployment, exactly one should be unpaused
				// If there's no active deployment (all finished), all should be paused
				if activeDeployment != "" {
					g.Expect(unpausedCount).To(Equal(1),
						"Expected exactly one unpaused deployment when active deployment exists. Active: %s, Unpaused: %v",
						activeDeployment, unpausedDeployments)
					// Verify the active deployment is the one that's unpaused
					// Extract just the name from the active deployment key (namespace/name)
					activeName := activeDeployment
					if strings.Contains(activeDeployment, "/") {
						parts := strings.Split(activeDeployment, "/")
						activeName = parts[len(parts)-1]
					}
					g.Expect(unpausedDeployments).To(ContainElement(activeName),
						"Active deployment %s should be unpaused", activeDeployment)
				} else {
					// No active deployment - all should be paused (or at most one might be finishing)
					g.Expect(unpausedCount).To(BeNumerically("<=", 1),
						"Expected at most one unpaused deployment when no active deployment. Unpaused: %v",
						unpausedDeployments)
				}
			}
			Eventually(verifyCoordination, 2*time.Minute, 5*time.Second).Should(Succeed())

			By("verifying that deployments are listed in status")
			verifyStatus := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "deploymentcoordination", coordinationName,
					"-n", testNamespace, "-o", "jsonpath={.status.deployments}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(ContainSubstring("deployment-1"))
				g.Expect(output).To(ContainSubstring("deployment-2"))
				g.Expect(output).To(ContainSubstring("deployment-3"))
			}
			Eventually(verifyStatus, 30*time.Second, 2*time.Second).Should(Succeed())
		})

		It("should respect MinReadySeconds before activating next deployment", func() {
			By("creating a DeploymentCoordination with MinReadySeconds")
			coordinationYAML := fmt.Sprintf(`apiVersion: apps.containerinfra.nl/v1
kind: DeploymentCoordination
metadata:
  name: %s
  namespace: %s
spec:
  labelSelector:
    matchLabels:
      coordinated: "true"
  minReadySeconds: 10
`, coordinationName, testNamespace)
			coordinationFile := writeTempFile(coordinationYAML)
			defer os.Remove(coordinationFile)

			cmd := exec.Command("kubectl", "apply", "-f", coordinationFile)
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to create DeploymentCoordination")

			By("creating two deployments")
			for i := 1; i <= 2; i++ {
				deploymentYAML := fmt.Sprintf(`apiVersion: apps/v1
kind: Deployment
metadata:
  name: deployment-%d
  namespace: %s
  labels:
    coordinated: "true"
spec:
  replicas: 1
  selector:
    matchLabels:
      app: deployment-%d
  template:
    metadata:
      labels:
        app: deployment-%d
    spec:
      containers:
      - name: nginx
        image: nginx:1.21
        ports:
        - containerPort: 80
`, i, testNamespace, i, i)
				deploymentFile := writeTempFile(deploymentYAML)
				defer os.Remove(deploymentFile)

				cmd = exec.Command("kubectl", "apply", "-f", deploymentFile)
				_, err = utils.Run(cmd)
				Expect(err).NotTo(HaveOccurred(), "Failed to create deployment-%d", i)
			}

			By("waiting for first deployment to be active and ready")
			verifyFirstDeploymentReady := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "deployment", "deployment-1",
					"-n", testNamespace, "-o", "jsonpath={.status.readyReplicas}")
				readyReplicas, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(readyReplicas).To(Equal("1"), "deployment-1 should have 1 ready replica")
			}
			Eventually(verifyFirstDeploymentReady, 2*time.Minute, 5*time.Second).Should(Succeed())

			By("verifying that second deployment remains paused during MinReadySeconds")
			// Immediately after first deployment is ready, second should still be paused
			cmd = exec.Command("kubectl", "get", "deployment", "deployment-2",
				"-n", testNamespace, "-o", "jsonpath={.spec.paused}")
			paused, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())
			Expect(paused).To(Equal("true"), "deployment-2 should be paused during MinReadySeconds")
		})

		It("should update status conditions correctly", func() {
			By("creating a DeploymentCoordination resource")
			coordinationYAML := fmt.Sprintf(`apiVersion: apps.containerinfra.nl/v1
kind: DeploymentCoordination
metadata:
  name: %s
  namespace: %s
spec:
  labelSelector:
    matchLabels:
      coordinated: "true"
  minReadySeconds: 0
`, coordinationName, testNamespace)
			coordinationFile := writeTempFile(coordinationYAML)
			defer os.Remove(coordinationFile)

			cmd := exec.Command("kubectl", "apply", "-f", coordinationFile)
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to create DeploymentCoordination")

			By("creating a deployment")
			deploymentYAML := fmt.Sprintf(`apiVersion: apps/v1
kind: Deployment
metadata:
  name: deployment-1
  namespace: %s
  labels:
    coordinated: "true"
spec:
  replicas: 1
  selector:
    matchLabels:
      app: deployment-1
  template:
    metadata:
      labels:
        app: deployment-1
    spec:
      containers:
      - name: nginx
        image: nginx:1.21
        ports:
        - containerPort: 80
`, testNamespace)
			deploymentFile := writeTempFile(deploymentYAML)
			defer os.Remove(deploymentFile)

			cmd = exec.Command("kubectl", "apply", "-f", deploymentFile)
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to create deployment")

			By("verifying that Progressing condition is set when deployment is rolling out")
			verifyProgressingCondition := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "deploymentcoordination", coordinationName,
					"-n", testNamespace, "-o", "jsonpath={.status.conditions[?(@.type==\"Progressing\")].status}")
				status, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(status).To(Equal("True"), "Progressing condition should be True when rolling out")
			}
			Eventually(verifyProgressingCondition, 30*time.Second, 2*time.Second).Should(Succeed())

			By("waiting for deployment to be ready")
			verifyDeploymentReady := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "deployment", "deployment-1",
					"-n", testNamespace, "-o", "jsonpath={.status.readyReplicas}")
				readyReplicas, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(readyReplicas).To(Equal("1"), "deployment should have 1 ready replica")
			}
			Eventually(verifyDeploymentReady, 2*time.Minute, 5*time.Second).Should(Succeed())

			By("verifying that Ready condition is set when all deployments are ready")
			verifyReadyCondition := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "deploymentcoordination", coordinationName,
					"-n", testNamespace, "-o", "jsonpath={.status.conditions[?(@.type==\"Ready\")].status}")
				status, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(status).To(Equal("True"), "Ready condition should be True when all deployments are ready")
			}
			Eventually(verifyReadyCondition, 30*time.Second, 2*time.Second).Should(Succeed())
		})

		It("should track rollout timestamps in deployment states", func() {
			By("creating a DeploymentCoordination resource")
			coordinationYAML := fmt.Sprintf(`apiVersion: apps.containerinfra.nl/v1
kind: DeploymentCoordination
metadata:
  name: %s
  namespace: %s
spec:
  labelSelector:
    matchLabels:
      coordinated: "true"
  minReadySeconds: 0
`, coordinationName, testNamespace)
			coordinationFile := writeTempFile(coordinationYAML)
			defer os.Remove(coordinationFile)

			cmd := exec.Command("kubectl", "apply", "-f", coordinationFile)
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to create DeploymentCoordination")

			By("creating a deployment")
			deploymentYAML := fmt.Sprintf(`apiVersion: apps/v1
kind: Deployment
metadata:
  name: deployment-1
  namespace: %s
  labels:
    coordinated: "true"
spec:
  replicas: 1
  selector:
    matchLabels:
      app: deployment-1
  template:
    metadata:
      labels:
        app: deployment-1
    spec:
      containers:
      - name: nginx
        image: nginx:1.21
        ports:
        - containerPort: 80
`, testNamespace)
			deploymentFile := writeTempFile(deploymentYAML)
			defer os.Remove(deploymentFile)

			cmd = exec.Command("kubectl", "apply", "-f", deploymentFile)
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to create deployment")

			By("verifying that LastRolloutStarted timestamp is set")
			verifyRolloutStarted := func(g Gomega) {
				deploymentKey := fmt.Sprintf("%s/deployment-1", testNamespace)
				jsonpath := fmt.Sprintf("{.status.deploymentStates[?(@.name==\"%s\")].lastRolloutStarted}", deploymentKey)
				cmd := exec.Command("kubectl", "get", "deploymentcoordination", coordinationName,
					"-n", testNamespace, "-o", fmt.Sprintf("jsonpath=%s", jsonpath))
				timestamp, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(timestamp).NotTo(BeEmpty(), "LastRolloutStarted should be set")
			}
			Eventually(verifyRolloutStarted, 30*time.Second, 2*time.Second).Should(Succeed())

			By("waiting for deployment to be ready")
			verifyDeploymentReady := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "deployment", "deployment-1",
					"-n", testNamespace, "-o", "jsonpath={.status.readyReplicas}")
				readyReplicas, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(readyReplicas).To(Equal("1"), "deployment should have 1 ready replica")
			}
			Eventually(verifyDeploymentReady, 2*time.Minute, 5*time.Second).Should(Succeed())

			By("verifying that LastRolloutFinished timestamp is set")
			verifyRolloutFinished := func(g Gomega) {
				deploymentKey := fmt.Sprintf("%s/deployment-1", testNamespace)
				jsonpath := fmt.Sprintf("{.status.deploymentStates[?(@.name==\"%s\")].lastRolloutFinished}", deploymentKey)
				cmd := exec.Command("kubectl", "get", "deploymentcoordination", coordinationName,
					"-n", testNamespace, "-o", fmt.Sprintf("jsonpath=%s", jsonpath))
				timestamp, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(timestamp).NotTo(BeEmpty(), "LastRolloutFinished should be set")
			}
			Eventually(verifyRolloutFinished, 30*time.Second, 2*time.Second).Should(Succeed())
		})

		It("should pause deployments via webhook when they match coordination", func() {
			By("creating a DeploymentCoordination resource")
			coordinationYAML := fmt.Sprintf(`apiVersion: apps.containerinfra.nl/v1
kind: DeploymentCoordination
metadata:
  name: %s
  namespace: %s
spec:
  labelSelector:
    matchLabels:
      coordinated: "true"
  minReadySeconds: 0
`, coordinationName, testNamespace)
			coordinationFile := writeTempFile(coordinationYAML)
			defer os.Remove(coordinationFile)

			cmd := exec.Command("kubectl", "apply", "-f", coordinationFile)
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to create DeploymentCoordination")

			By("creating a deployment that matches the label selector")
			deploymentYAML := fmt.Sprintf(`apiVersion: apps/v1
kind: Deployment
metadata:
  name: deployment-1
  namespace: %s
  labels:
    coordinated: "true"
spec:
  replicas: 1
  selector:
    matchLabels:
      app: deployment-1
  template:
    metadata:
      labels:
        app: deployment-1
    spec:
      containers:
      - name: nginx
        image: nginx:1.21
        ports:
        - containerPort: 80
`, testNamespace)
			deploymentFile := writeTempFile(deploymentYAML)
			defer os.Remove(deploymentFile)

			cmd = exec.Command("kubectl", "apply", "-f", deploymentFile)
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to create deployment")

			By("verifying that the deployment is paused by the webhook")
			verifyPaused := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "deployment", "deployment-1",
					"-n", testNamespace, "-o", "jsonpath={.spec.paused}")
				paused, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				// The webhook should pause it, or the controller will pause it
				// Either way, it should be paused initially
				g.Expect(paused).To(Equal("true"), "deployment should be paused by webhook or controller")
			}
			Eventually(verifyPaused, 10*time.Second, 1*time.Second).Should(Succeed())
		})
	})
})

// writeTempFile writes content to a temporary file and returns the file path
func writeTempFile(content string) string {
	tmpfile, err := os.CreateTemp("", "e2e-test-*.yaml")
	Expect(err).NotTo(HaveOccurred())
	_, err = tmpfile.WriteString(content)
	Expect(err).NotTo(HaveOccurred())
	err = tmpfile.Close()
	Expect(err).NotTo(HaveOccurred())
	return tmpfile.Name()
}

// serviceAccountToken returns a token for the specified service account in the given namespace.
// It uses the Kubernetes TokenRequest API to generate a token by directly sending a request
// and parsing the resulting token from the API response.
func serviceAccountToken() (string, error) {
	const tokenRequestRawString = `{
		"apiVersion": "authentication.k8s.io/v1",
		"kind": "TokenRequest"
	}`

	// Temporary file to store the token request
	secretName := fmt.Sprintf("%s-token-request", serviceAccountName)
	tokenRequestFile := filepath.Join("/tmp", secretName)
	err := os.WriteFile(tokenRequestFile, []byte(tokenRequestRawString), os.FileMode(0o644))
	if err != nil {
		return "", err
	}

	var out string
	verifyTokenCreation := func(g Gomega) {
		// Execute kubectl command to create the token
		cmd := exec.Command("kubectl", "create", "--raw", fmt.Sprintf(
			"/api/v1/namespaces/%s/serviceaccounts/%s/token",
			namespace,
			serviceAccountName,
		), "-f", tokenRequestFile)

		output, err := cmd.CombinedOutput()
		g.Expect(err).NotTo(HaveOccurred())

		// Parse the JSON output to extract the token
		var token tokenRequest
		err = json.Unmarshal(output, &token)
		g.Expect(err).NotTo(HaveOccurred())

		out = token.Status.Token
	}
	Eventually(verifyTokenCreation).Should(Succeed())

	return out, err
}

// getMetricsOutput retrieves and returns the logs from the curl pod used to access the metrics endpoint.
func getMetricsOutput() string {
	By("getting the curl-metrics logs")
	cmd := exec.Command("kubectl", "logs", "curl-metrics", "-n", namespace)
	metricsOutput, err := utils.Run(cmd)
	Expect(err).NotTo(HaveOccurred(), "Failed to retrieve logs from curl pod")
	Expect(metricsOutput).To(ContainSubstring("< HTTP/1.1 200 OK"))
	return metricsOutput
}

// tokenRequest is a simplified representation of the Kubernetes TokenRequest API response,
// containing only the token field that we need to extract.
type tokenRequest struct {
	Status struct {
		Token string `json:"token"`
	} `json:"status"`
}
