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

package v1

import (
	"context"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	appsv1k8s "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	appsv1 "github.com/containerinfra/kube-deployment-coordinator/api/v1"
)

func TestWebhooks(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Webhook Suite")
}

var _ = Describe("Deployment Webhook", func() {
	var (
		webhook *Deployment
		ctx     context.Context
		scheme  *runtime.Scheme
	)

	BeforeEach(func() {
		ctx = context.Background()
		scheme = runtime.NewScheme()
		Expect(appsv1k8s.AddToScheme(scheme)).To(Succeed())
		Expect(appsv1.AddToScheme(scheme)).To(Succeed())
	})

	Describe("Default", func() {
		Context("when deployment does not match any DeploymentCoordination", func() {
			It("should not pause the deployment", func() {
				deployment := &appsv1k8s.Deployment{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-deployment",
						Namespace: "default",
						Labels: map[string]string{
							"app": "test",
						},
					},
					Spec: appsv1k8s.DeploymentSpec{
						Paused: false,
					},
				}

				fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
				webhook = &Deployment{Client: fakeClient}

				err := webhook.Default(ctx, deployment)
				Expect(err).NotTo(HaveOccurred())
				Expect(deployment.Spec.Paused).To(BeFalse())
			})
		})

		Context("when deployment matches DeploymentCoordination but is not active", func() {
			It("should pause the deployment", func() {
				deployment := &appsv1k8s.Deployment{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-deployment",
						Namespace: "default",
						Labels: map[string]string{
							"app":         "test",
							"coordinated": "true",
						},
					},
					Spec: appsv1k8s.DeploymentSpec{
						Paused: false,
					},
				}

				coordination := &appsv1.DeploymentCoordination{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-coordination",
						Namespace: "default",
					},
					Spec: appsv1.DeploymentCoordinationSpec{
						LabelSelector: metav1.LabelSelector{
							MatchLabels: map[string]string{
								"coordinated": "true",
							},
						},
					},
					Status: appsv1.DeploymentCoordinationStatus{
						ActiveDeployment: "", // Not active
					},
				}

				fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(coordination).Build()
				webhook = &Deployment{Client: fakeClient}

				err := webhook.Default(ctx, deployment)
				Expect(err).NotTo(HaveOccurred())
				Expect(deployment.Spec.Paused).To(BeTrue())
			})
		})

		Context("when deployment matches DeploymentCoordination and is active", func() {
			It("should not pause the deployment", func() {
				deployment := &appsv1k8s.Deployment{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-deployment",
						Namespace: "default",
						Labels: map[string]string{
							"app":         "test",
							"coordinated": "true",
						},
					},
					Spec: appsv1k8s.DeploymentSpec{
						Paused: false,
					},
				}

				coordination := &appsv1.DeploymentCoordination{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-coordination",
						Namespace: "default",
					},
					Spec: appsv1.DeploymentCoordinationSpec{
						LabelSelector: metav1.LabelSelector{
							MatchLabels: map[string]string{
								"coordinated": "true",
							},
						},
					},
					Status: appsv1.DeploymentCoordinationStatus{
						ActiveDeployment: "default/test-deployment", // This deployment is active
					},
				}

				fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(coordination).Build()
				webhook = &Deployment{Client: fakeClient}

				err := webhook.Default(ctx, deployment)
				Expect(err).NotTo(HaveOccurred())
				Expect(deployment.Spec.Paused).To(BeFalse())
			})
		})

		Context("when deployment is already paused and matches DeploymentCoordination", func() {
			It("should keep the deployment paused", func() {
				deployment := &appsv1k8s.Deployment{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-deployment",
						Namespace: "default",
						Labels: map[string]string{
							"app":         "test",
							"coordinated": "true",
						},
					},
					Spec: appsv1k8s.DeploymentSpec{
						Paused: true,
					},
				}

				coordination := &appsv1.DeploymentCoordination{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-coordination",
						Namespace: "default",
					},
					Spec: appsv1.DeploymentCoordinationSpec{
						LabelSelector: metav1.LabelSelector{
							MatchLabels: map[string]string{
								"coordinated": "true",
							},
						},
					},
					Status: appsv1.DeploymentCoordinationStatus{
						ActiveDeployment: "", // Not active
					},
				}

				fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(coordination).Build()
				webhook = &Deployment{Client: fakeClient}

				err := webhook.Default(ctx, deployment)
				Expect(err).NotTo(HaveOccurred())
				Expect(deployment.Spec.Paused).To(BeTrue())
			})
		})

		Context("when deployment matches multiple DeploymentCoordinations", func() {
			It("should not pause if active in any coordination", func() {
				deployment := &appsv1k8s.Deployment{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-deployment",
						Namespace: "default",
						Labels: map[string]string{
							"app":         "test",
							"coordinated": "true",
						},
					},
					Spec: appsv1k8s.DeploymentSpec{
						Paused: false,
					},
				}

				coordination1 := &appsv1.DeploymentCoordination{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-coordination-1",
						Namespace: "default",
					},
					Spec: appsv1.DeploymentCoordinationSpec{
						LabelSelector: metav1.LabelSelector{
							MatchLabels: map[string]string{
								"coordinated": "true",
							},
						},
					},
					Status: appsv1.DeploymentCoordinationStatus{
						ActiveDeployment: "", // Not active in this one
					},
				}

				coordination2 := &appsv1.DeploymentCoordination{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-coordination-2",
						Namespace: "default",
					},
					Spec: appsv1.DeploymentCoordinationSpec{
						LabelSelector: metav1.LabelSelector{
							MatchLabels: map[string]string{
								"coordinated": "true",
							},
						},
					},
					Status: appsv1.DeploymentCoordinationStatus{
						ActiveDeployment: "default/test-deployment", // Active in this one
					},
				}

				fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(coordination1, coordination2).Build()
				webhook = &Deployment{Client: fakeClient}

				err := webhook.Default(ctx, deployment)
				Expect(err).NotTo(HaveOccurred())
				Expect(deployment.Spec.Paused).To(BeFalse())
			})
		})

		Context("when client is nil", func() {
			It("should not fail and skip coordination check", func() {
				deployment := &appsv1k8s.Deployment{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-deployment",
						Namespace: "default",
					},
					Spec: appsv1k8s.DeploymentSpec{
						Paused: false,
					},
				}

				webhook = &Deployment{Client: nil}

				err := webhook.Default(ctx, deployment)
				Expect(err).NotTo(HaveOccurred())
				Expect(deployment.Spec.Paused).To(BeFalse())
			})
		})

		Context("when object is not a Deployment", func() {
			It("should return an error", func() {
				webhook = &Deployment{Client: fake.NewClientBuilder().WithScheme(scheme).Build()}

				err := webhook.Default(ctx, &appsv1.DeploymentCoordination{})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("expected *appsv1k8s.Deployment"))
			})
		})

		Context("when label selector uses matchExpressions", func() {
			It("should match deployments correctly", func() {
				deployment := &appsv1k8s.Deployment{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-deployment",
						Namespace: "default",
						Labels: map[string]string{
							"app": "test",
							"env": "prod",
						},
					},
					Spec: appsv1k8s.DeploymentSpec{
						Paused: false,
					},
				}

				coordination := &appsv1.DeploymentCoordination{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-coordination",
						Namespace: "default",
					},
					Spec: appsv1.DeploymentCoordinationSpec{
						LabelSelector: metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								{
									Key:      "app",
									Operator: metav1.LabelSelectorOpIn,
									Values:   []string{"test"},
								},
								{
									Key:      "env",
									Operator: metav1.LabelSelectorOpIn,
									Values:   []string{"prod", "staging"},
								},
							},
						},
					},
					Status: appsv1.DeploymentCoordinationStatus{
						ActiveDeployment: "",
					},
				}

				fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(coordination).Build()
				webhook = &Deployment{Client: fakeClient}

				err := webhook.Default(ctx, deployment)
				Expect(err).NotTo(HaveOccurred())
				Expect(deployment.Spec.Paused).To(BeTrue())
			})
		})

		Context("when deployment is in different namespace", func() {
			It("should only match coordinations in the same namespace", func() {
				deployment := &appsv1k8s.Deployment{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-deployment",
						Namespace: "namespace1",
						Labels: map[string]string{
							"coordinated": "true",
						},
					},
					Spec: appsv1k8s.DeploymentSpec{
						Paused: false,
					},
				}

				// Coordination in different namespace
				coordination := &appsv1.DeploymentCoordination{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-coordination",
						Namespace: "namespace2",
					},
					Spec: appsv1.DeploymentCoordinationSpec{
						LabelSelector: metav1.LabelSelector{
							MatchLabels: map[string]string{
								"coordinated": "true",
							},
						},
					},
					Status: appsv1.DeploymentCoordinationStatus{
						ActiveDeployment: "",
					},
				}

				fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(coordination).Build()
				webhook = &Deployment{Client: fakeClient}

				err := webhook.Default(ctx, deployment)
				Expect(err).NotTo(HaveOccurred())
				Expect(deployment.Spec.Paused).To(BeFalse()) // Should not be paused since no match in same namespace
			})
		})
	})
})
