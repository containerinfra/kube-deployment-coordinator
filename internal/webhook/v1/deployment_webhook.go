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
	"fmt"

	appsv1k8s "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	appsv1 "github.com/containerinfra/kube-deployment-coordinator/api/v1"
)

// nolint:unused
// log is for logging in this package.
var deploymentlog = logf.Log.WithName("deployment-resource")

// Deployment is a webhook for Deployment resources.
// +kubebuilder:webhook:path=/mutate-apps-v1-deployment,mutating=true,failurePolicy=fail,sideEffects=None,groups=apps,resources=deployments,verbs=create;update,versions=v1,name=mdeployment.kb.io,admissionReviewVersions=v1
type Deployment struct {
	appsv1k8s.Deployment
	Client client.Client
}

// Default implements webhook.CustomDefaulter so a webhook will be registered for the type.
func (r *Deployment) Default(ctx context.Context, obj runtime.Object) error {
	deployment, ok := obj.(*appsv1k8s.Deployment)
	if !ok {
		return fmt.Errorf("expected *appsv1k8s.Deployment, got %T", obj)
	}
	deploymentlog.Info("default", "name", deployment.GetName(), "namespace", deployment.GetNamespace())

	// If no client, skip coordination check (should not happen in production)
	if r.Client == nil {
		deploymentlog.V(1).Info("webhook client not available, skipping coordination check")
		return nil
	}

	// Find DeploymentCoordination resources that match this deployment
	coordinations, err := r.findMatchingCoordinations(ctx, deployment)
	if err != nil {
		deploymentlog.Error(err, "unable to find matching DeploymentCoordination resources")
		// Don't fail the webhook, just log the error
		return nil
	}

	// If no matching coordinations, nothing to do
	if len(coordinations) == 0 {
		deploymentlog.V(1).Info("deployment does not match any DeploymentCoordination", "name", deployment.GetName())
		return nil
	}

	deploymentKey := fmt.Sprintf("%s/%s", deployment.GetNamespace(), deployment.GetName())

	// Check if this deployment is the active one in any coordination
	for _, coordination := range coordinations {
		if coordination.Status.ActiveDeployment == deploymentKey {
			// This deployment is active, don't pause it
			deploymentlog.V(1).Info("deployment is active in DeploymentCoordination, skipping webhook pause",
				"name", deployment.GetName(), "coordination", coordination.Name)
			return nil
		}
	}

	// Deployment is coordinated but not active
	// Ensure it starts paused - the controller will manage unpausing when appropriate
	if deployment.Spec.Paused {
		deploymentlog.V(1).Info("deployment is coordinated and paused, controller will manage",
			"name", deployment.GetName())
		return nil
	}

	// Deployment is not paused, pause it so the controller can manage the rollout
	deployment.Spec.Paused = true
	deploymentlog.Info("paused coordinated deployment (will be managed by controller)",
		"name", deployment.GetName(), "namespace", deployment.GetNamespace())
	return nil
}

// findMatchingCoordinations finds all DeploymentCoordination resources that match this deployment.
func (r *Deployment) findMatchingCoordinations(ctx context.Context, deployment *appsv1k8s.Deployment) ([]appsv1.DeploymentCoordination, error) {
	var coordinations appsv1.DeploymentCoordinationList
	if err := r.Client.List(ctx, &coordinations, client.InNamespace(deployment.GetNamespace())); err != nil {
		return nil, err
	}

	matching := make([]appsv1.DeploymentCoordination, 0)
	deploymentLabels := labels.Set(deployment.GetLabels())

	for i := range coordinations.Items {
		coordination := &coordinations.Items[i]
		selector, err := metav1.LabelSelectorAsSelector(&coordination.Spec.LabelSelector)
		if err != nil {
			deploymentlog.V(1).Info("invalid label selector in DeploymentCoordination", "coordination", coordination.Name, "error", err)
			continue
		}

		if selector.Matches(deploymentLabels) {
			matching = append(matching, *coordination)
		}
	}

	return matching, nil
}

// SetupDeploymentWebhookWithManager registers the webhook for Deployment in the manager.
func SetupDeploymentWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		For(&appsv1k8s.Deployment{}).
		WithDefaulter(&Deployment{Client: mgr.GetClient()}).
		Complete()
}
