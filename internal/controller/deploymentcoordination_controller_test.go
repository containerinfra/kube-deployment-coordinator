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

package controller

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	appsv1k8s "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	appsv1 "github.com/containerinfra/kube-deployment-coordinator/api/v1"
)

var _ = Describe("DeploymentCoordination Controller", func() {
	Context("Happy flow - coordinating multiple deployments", func() {
		const (
			coordinationName = "test-coordination"
			namespace        = "default"
		)

		var (
			ctx                   context.Context
			coordination          *appsv1.DeploymentCoordination
			deployment1           *appsv1k8s.Deployment
			deployment2           *appsv1k8s.Deployment
			deployment3           *appsv1k8s.Deployment
			reconciler            *DeploymentCoordinationReconciler
			coordinationNamespace types.NamespacedName
		)

		BeforeEach(func() {
			ctx = context.Background()
			coordinationNamespace = types.NamespacedName{
				Name:      coordinationName,
				Namespace: namespace,
			}

			// Create a fake event recorder for testing
			eventRecorder := record.NewFakeRecorder(100)

			reconciler = &DeploymentCoordinationReconciler{
				Client:   k8sClient,
				Scheme:   k8sClient.Scheme(),
				Recorder: eventRecorder,
			}

			By("Creating a DeploymentCoordination resource")
			coordination = &appsv1.DeploymentCoordination{
				ObjectMeta: metav1.ObjectMeta{
					Name:      coordinationName,
					Namespace: namespace,
				},
				Spec: appsv1.DeploymentCoordinationSpec{
					LabelSelector: metav1.LabelSelector{
						MatchLabels: map[string]string{
							"coordinated": "true",
						},
					},
					MinReadySeconds: 0, // No wait time for tests
				},
			}
			err := k8sClient.Create(ctx, coordination)
			if err != nil && !errors.IsAlreadyExists(err) {
				Expect(err).NotTo(HaveOccurred())
			}

			By("Creating three deployments that match the label selector")
			replicas := int32(1)

			// Deployment 1 - needs rollout
			deployment1 = &appsv1k8s.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "deployment-1",
					Namespace: namespace,
					Labels: map[string]string{
						"coordinated": "true",
					},
				},
				Spec: appsv1k8s.DeploymentSpec{
					Replicas: &replicas,
					Selector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"app": "deployment-1",
						},
					},
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Labels: map[string]string{
								"app": "deployment-1",
							},
						},
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name:  "test",
									Image: "nginx:latest",
								},
							},
						},
					},
					Paused: true, // Start paused
				},
			}
			Expect(k8sClient.Create(ctx, deployment1)).To(Succeed())
			// Update status to indicate it needs rollout (ObservedGeneration < Generation)
			// Also set replica counts to 0 to indicate it hasn't rolled out yet
			deployment1.Status.ObservedGeneration = 0
			deployment1.Status.Replicas = 0
			deployment1.Status.UpdatedReplicas = 0
			deployment1.Status.ReadyReplicas = 0
			deployment1.Status.AvailableReplicas = 0
			Expect(k8sClient.Status().Update(ctx, deployment1)).To(Succeed())

			// Deployment 2 - needs rollout
			deployment2 = &appsv1k8s.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "deployment-2",
					Namespace: namespace,
					Labels: map[string]string{
						"coordinated": "true",
					},
				},
				Spec: appsv1k8s.DeploymentSpec{
					Replicas: &replicas,
					Selector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"app": "deployment-2",
						},
					},
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Labels: map[string]string{
								"app": "deployment-2",
							},
						},
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name:  "test",
									Image: "nginx:latest",
								},
							},
						},
					},
					Paused: true, // Start paused
				},
			}
			Expect(k8sClient.Create(ctx, deployment2)).To(Succeed())
			// Update status to indicate it needs rollout (ObservedGeneration < Generation)
			// Also set replica counts to 0 to indicate it hasn't rolled out yet
			deployment2.Status.ObservedGeneration = 0
			deployment2.Status.Replicas = 0
			deployment2.Status.UpdatedReplicas = 0
			deployment2.Status.ReadyReplicas = 0
			deployment2.Status.AvailableReplicas = 0
			Expect(k8sClient.Status().Update(ctx, deployment2)).To(Succeed())

			// Deployment 3 - needs rollout
			deployment3 = &appsv1k8s.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "deployment-3",
					Namespace: namespace,
					Labels: map[string]string{
						"coordinated": "true",
					},
				},
				Spec: appsv1k8s.DeploymentSpec{
					Replicas: &replicas,
					Selector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"app": "deployment-3",
						},
					},
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Labels: map[string]string{
								"app": "deployment-3",
							},
						},
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name:  "test",
									Image: "nginx:latest",
								},
							},
						},
					},
					Paused: true, // Start paused
				},
			}
			Expect(k8sClient.Create(ctx, deployment3)).To(Succeed())
			// Update status to indicate it needs rollout (ObservedGeneration < Generation)
			// Also set replica counts to 0 to indicate it hasn't rolled out yet
			deployment3.Status.ObservedGeneration = 0
			deployment3.Status.Replicas = 0
			deployment3.Status.UpdatedReplicas = 0
			deployment3.Status.ReadyReplicas = 0
			deployment3.Status.AvailableReplicas = 0
			Expect(k8sClient.Status().Update(ctx, deployment3)).To(Succeed())
		})

		AfterEach(func() {
			By("Cleaning up deployments")
			if deployment1 != nil {
				Expect(k8sClient.Delete(ctx, deployment1)).To(Succeed())
			}
			if deployment2 != nil {
				Expect(k8sClient.Delete(ctx, deployment2)).To(Succeed())
			}
			if deployment3 != nil {
				Expect(k8sClient.Delete(ctx, deployment3)).To(Succeed())
			}

			By("Cleaning up DeploymentCoordination")
			if coordination != nil {
				Expect(k8sClient.Delete(ctx, coordination)).To(Succeed())
			}
		})

		It("should coordinate deployments and activate them one at a time", func() {
			By("Reconciling the DeploymentCoordination")
			result, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: coordinationNamespace,
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeZero())

			By("Verifying the first deployment is active and unpaused")
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "deployment-1", Namespace: namespace}, deployment1)).To(Succeed())
			Expect(deployment1.Spec.Paused).To(BeFalse(), "first deployment should be unpaused")

			By("Verifying other deployments are paused")
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "deployment-2", Namespace: namespace}, deployment2)).To(Succeed())
			Expect(deployment2.Spec.Paused).To(BeTrue(), "second deployment should be paused")
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "deployment-3", Namespace: namespace}, deployment3)).To(Succeed())
			Expect(deployment3.Spec.Paused).To(BeTrue(), "third deployment should be paused")

			By("Verifying the status shows deployment-1 as active")
			Expect(k8sClient.Get(ctx, coordinationNamespace, coordination)).To(Succeed())
			Expect(coordination.Status.ActiveDeployment).To(Equal("default/deployment-1"))
			Expect(coordination.Status.Deployments).To(ContainElements("default/deployment-1", "default/deployment-2", "default/deployment-3"))

			By("Simulating deployment-1 finishing rollout")
			// Refresh deployment1 to get the current Generation
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "deployment-1", Namespace: namespace}, deployment1)).To(Succeed())
			deployment1.Status.ObservedGeneration = deployment1.Generation
			deployment1.Status.Replicas = 1
			deployment1.Status.UpdatedReplicas = 1
			deployment1.Status.ReadyReplicas = 1
			deployment1.Status.AvailableReplicas = 1
			deployment1.Status.Conditions = []appsv1k8s.DeploymentCondition{
				{
					Type:               appsv1k8s.DeploymentAvailable,
					Status:             corev1.ConditionTrue,
					LastTransitionTime: metav1.Now(),
				},
			}
			Expect(k8sClient.Status().Update(ctx, deployment1)).To(Succeed())

			By("Reconciling again to activate the next deployment")
			result, err = reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: coordinationNamespace,
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeZero())

			By("Verifying deployment-1 is no longer active")
			Expect(k8sClient.Get(ctx, coordinationNamespace, coordination)).To(Succeed())
			Expect(coordination.Status.ActiveDeployment).To(Equal("default/deployment-2"))

			By("Verifying deployment-2 is now active and unpaused")
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "deployment-2", Namespace: namespace}, deployment2)).To(Succeed())
			Expect(deployment2.Spec.Paused).To(BeFalse(), "second deployment should be unpaused")

			By("Verifying deployment-3 is still paused")
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "deployment-3", Namespace: namespace}, deployment3)).To(Succeed())
			Expect(deployment3.Spec.Paused).To(BeTrue(), "third deployment should still be paused")
		})
	})
})
