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
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/go-logr/logr"
	appsv1k8s "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	appsv1 "github.com/containerinfra/kube-deployment-coordinator/api/v1"
)

// DeploymentCoordinationReconciler reconciles a DeploymentCoordination object
type DeploymentCoordinationReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

// +kubebuilder:rbac:groups=apps.containerinfra.nl,resources=deploymentcoordinations,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=apps.containerinfra.nl,resources=deploymentcoordinations/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=apps.containerinfra.nl,resources=deploymentcoordinations/finalizers,verbs=update
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// It coordinates deployments matching the label selector to ensure only one
// deployment rolls out at a time.
//
//nolint:gocyclo
func (r *DeploymentCoordinationReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var coordination appsv1.DeploymentCoordination
	if err := r.Get(ctx, req.NamespacedName, &coordination); err != nil {
		logger.Error(err, "unable to fetch DeploymentCoordination")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	logger.Info("reconciling DeploymentCoordination", "name", coordination.Name)

	// Build label selector
	selector, err := metav1.LabelSelectorAsSelector(&coordination.Spec.LabelSelector)
	if err != nil {
		logger.Error(err, "invalid label selector")
		return ctrl.Result{}, err
	}

	// Find all deployments matching the label selector in the same namespace
	var deployments appsv1k8s.DeploymentList
	if err := r.List(ctx, &deployments, client.InNamespace(coordination.Namespace), client.MatchingLabelsSelector{Selector: selector}); err != nil {
		logger.Error(err, "unable to list deployments")
		return ctrl.Result{}, err
	}

	// Build list of deployment keys
	deploymentKeys := make([]string, 0, len(deployments.Items))
	deploymentMap := make(map[string]*appsv1k8s.Deployment)
	for i := range deployments.Items {
		deployment := &deployments.Items[i]
		key := getDeploymentKeyForCoordination(deployment)
		deploymentKeys = append(deploymentKeys, key)
		deploymentMap[key] = deployment
	}
	// Note: deploymentStates will be calculated later using recalculateDeploymentStates()
	// to ensure accuracy after all deployment updates are complete
	var deploymentStates []appsv1.DeploymentState

	// Get current active deployment from status
	activeDeploymentKey := coordination.Status.ActiveDeployment
	var previousActiveDeploymentKey string // Track if we just cleared an active deployment

	// Find the active deployment if it exists
	var activeDeployment *appsv1k8s.Deployment
	if activeDeploymentKey != "" {
		if dep, exists := deploymentMap[activeDeploymentKey]; exists {
			activeDeployment = dep
		} else {
			// Active deployment no longer exists or doesn't match selector, clear it
			logger.Info("active deployment no longer matches selector, clearing", "activeDeployment", activeDeploymentKey)
			r.Recorder.Eventf(&coordination, corev1.EventTypeNormal, "ActiveDeploymentCleared",
				"Active deployment %s no longer matches selector, cleared from coordination", activeDeploymentKey)
			previousActiveDeploymentKey = activeDeploymentKey
			activeDeploymentKey = ""
		}
	}

	// Check if active deployment is still rolling out or needs to wait for MinReadySeconds
	if activeDeployment != nil {
		if r.isRollingOut(activeDeployment) {
			// Active deployment is still rolling out, requeue to check again
			logger.V(1).Info("active deployment is still rolling out, requeuing", "deployment", activeDeploymentKey)
			return ctrl.Result{RequeueAfter: time.Second * 10}, nil
		}

		// Active deployment finished rolling out, check if it has been ready for MinReadySeconds
		minReadySeconds := coordination.Spec.MinReadySeconds
		if minReadySeconds > 0 {
			if !r.hasBeenReadyForMinTime(activeDeployment, minReadySeconds) {
				// Deployment finished rolling out but hasn't been ready for the minimum time yet
				logger.Info("active deployment finished rolling out but waiting for MinReadySeconds", "deployment", activeDeploymentKey, "minReadySeconds", minReadySeconds)
				// Requeue to check again after the minimum time has elapsed
				// Calculate how long to wait
				readyTime := r.getReadyTime(activeDeployment)
				if !readyTime.IsZero() {
					elapsed := time.Since(readyTime)
					remaining := time.Duration(minReadySeconds)*time.Second - elapsed
					if remaining > 0 {
						logger.Info("requeuing to wait for MinReadySeconds", "deployment", activeDeploymentKey, "remaining", remaining)
						return ctrl.Result{RequeueAfter: remaining}, nil
					}
				} else {
					// Can't determine ready time, but deployment is ready
					// Requeue after a short interval to check again
					logger.V(1).Info("cannot determine ready time, requeuing to check MinReadySeconds", "deployment", activeDeploymentKey)
					return ctrl.Result{RequeueAfter: time.Second * 5}, nil
				}
			}
		}
		// Active deployment finished rolling out and has been ready for MinReadySeconds (if required)
		logger.Info("active deployment finished rolling out and ready", "deployment", activeDeploymentKey)
		r.Recorder.Eventf(&coordination, corev1.EventTypeNormal, "ActiveDeploymentReady",
			"Active deployment %s finished rolling out and is ready", activeDeploymentKey)
		// Store the previously active deployment key to skip it in the next pass
		// This prevents immediately reactivating a deployment that just finished
		previousActiveDeploymentKey = activeDeploymentKey
		activeDeploymentKey = ""
		activeDeployment = nil
	}

	// Sort deployment keys for deterministic ordering
	sortedKeys := make([]string, 0, len(deploymentMap))
	for key := range deploymentMap {
		sortedKeys = append(sortedKeys, key)
	}
	sort.Strings(sortedKeys)

	// First pass: Handle active deployment and ensure all non-active deployments are paused
	for _, key := range sortedKeys {
		deployment := deploymentMap[key]
		isRollingOut := r.isRollingOut(deployment)

		// Handle active deployment
		if key == activeDeploymentKey {
			// Ensure status is updated with active deployment BEFORE unpausing
			// This prevents the webhook from pausing it again
			if coordination.Status.ActiveDeployment != activeDeploymentKey {
				logger.Info("updating status with active deployment before unpausing", "deployment", key)
				// Recalculate deployment states
				deploymentStates = r.recalculateDeploymentStates(sortedKeys, deploymentMap, coordination.Status.DeploymentStates, activeDeploymentKey, coordination.Status.ActiveDeployment)
				r.updateConditions(&coordination, activeDeploymentKey, r.hasPendingRollouts(deploymentMap, activeDeploymentKey), false)
				coordination.Status.ActiveDeployment = activeDeploymentKey
				coordination.Status.Deployments = deploymentKeys
				coordination.Status.DeploymentStates = deploymentStates
				if err := r.updateStatusWithRetry(ctx, &coordination, logger); err != nil {
					logger.Error(err, "unable to update DeploymentCoordination status before unpausing")
					return ctrl.Result{}, err
				}
			}
			// Now that status is updated, ensure active deployment is not paused if it needs to progress
			needsRollout := r.needsRollout(deployment)
			if deployment.Spec.Paused && needsRollout {
				logger.Info("unpausing active deployment (needs rollout)", "deployment", key)
				if err := r.updateDeploymentPaused(ctx, key, false, logger); err != nil {
					logger.Error(err, "unable to unpause active deployment", "deployment", key)
					r.Recorder.Eventf(&coordination, corev1.EventTypeWarning, "UnpauseFailed",
						"Failed to unpause active deployment %s: %v", key, err)
					r.updateConditions(&coordination, activeDeploymentKey, r.hasPendingRollouts(deploymentMap, activeDeploymentKey), true)
					if statusErr := r.updateStatusWithRetry(ctx, &coordination, logger); statusErr != nil {
						logger.Error(statusErr, "unable to update DeploymentCoordination status after unpause failure")
					}
					return ctrl.Result{}, err
				}
				// Refresh deployment in map after update
				if err := r.Get(ctx, types.NamespacedName{Namespace: deployment.Namespace, Name: deployment.Name}, deployment); err == nil {
					deploymentMap[key] = deployment
				}
				r.Recorder.Eventf(&coordination, corev1.EventTypeNormal, "DeploymentUnpaused",
					"Unpaused active deployment %s", key)
			} else if deployment.Spec.Paused && !needsRollout {
				logger.V(1).Info("active deployment is paused but doesn't need rollout, keeping paused", "deployment", key)
			}
			continue
		}

		// If there's an active deployment, ensure all others are paused
		if activeDeployment != nil {
			if !deployment.Spec.Paused {
				logger.Info("pausing deployment, another deployment is active", "deployment", key, "activeDeployment", activeDeploymentKey)
				if err := r.updateDeploymentPaused(ctx, key, true, logger); err != nil {
					logger.Error(err, "unable to pause deployment", "deployment", key)
					r.Recorder.Eventf(&coordination, corev1.EventTypeWarning, "PauseFailed",
						"Failed to pause deployment %s: %v", key, err)
					r.updateConditions(&coordination, activeDeploymentKey, r.hasPendingRollouts(deploymentMap, activeDeploymentKey), true)
					if statusErr := r.updateStatusWithRetry(ctx, &coordination, logger); statusErr != nil {
						logger.Error(statusErr, "unable to update DeploymentCoordination status after pause failure")
					}
					return ctrl.Result{}, err
				}
				// Refresh deployment in map after update
				if err := r.Get(ctx, types.NamespacedName{Namespace: deployment.Namespace, Name: deployment.Name}, deployment); err == nil {
					deploymentMap[key] = deployment
				}
				r.Recorder.Eventf(&coordination, corev1.EventTypeNormal, "DeploymentPaused",
					"Paused deployment %s (active deployment: %s)", key, activeDeploymentKey)
			}
			continue
		}

		// No active deployment - ensure non-rolling-out deployments are paused
		// We'll handle activation in the second pass
		if !isRollingOut && !deployment.Spec.Paused {
			logger.Info("pausing deployment (no active deployment, not rolling out)", "deployment", key)
			if err := r.updateDeploymentPaused(ctx, key, true, logger); err != nil {
				logger.Error(err, "unable to pause deployment", "deployment", key)
				r.Recorder.Eventf(&coordination, corev1.EventTypeWarning, "PauseFailed",
					"Failed to pause deployment %s: %v", key, err)
				r.updateConditions(&coordination, "", r.hasPendingRollouts(deploymentMap, ""), true)
				if statusErr := r.updateStatusWithRetry(ctx, &coordination, logger); statusErr != nil {
					logger.Error(statusErr, "unable to update DeploymentCoordination status after pause failure")
				}
				return ctrl.Result{}, err
			}
			// Refresh deployment in map after update
			if err := r.Get(ctx, types.NamespacedName{Namespace: deployment.Namespace, Name: deployment.Name}, deployment); err == nil {
				deploymentMap[key] = deployment
			}
			r.Recorder.Eventf(&coordination, corev1.EventTypeNormal, "DeploymentPaused",
				"Paused deployment %s (no active deployment, not rolling out)", key)
		}
	}

	// Refresh all deployments in the map before second pass to ensure we have latest status
	// This is important after pausing/unpausing operations
	for _, key := range sortedKeys {
		if err := r.Get(ctx, types.NamespacedName{
			Namespace: deploymentMap[key].Namespace,
			Name:      deploymentMap[key].Name,
		}, deploymentMap[key]); err != nil {
			logger.V(1).Info("unable to refresh deployment in map", "deployment", key, "error", err)
		}
	}

	// Second pass: Determine which deployment should be active (if any)
	// This ensures deterministic activation order based on sorted keys
	if activeDeployment == nil {
		// Check if there's already a deployment rolling out (shouldn't happen, but handle it)
		for _, key := range sortedKeys {
			deployment := deploymentMap[key]
			isRollingOut := r.isRollingOut(deployment)
			if isRollingOut {
				logger.Info("deployment is rolling out, setting as active", "deployment", key)
				activeDeploymentKey = key
				r.Recorder.Eventf(&coordination, corev1.EventTypeNormal, "DeploymentActivated",
					"Activated deployment %s (already rolling out)", key)
				// Recalculate deployment states before updating status
				deploymentStates = r.recalculateDeploymentStates(sortedKeys, deploymentMap, coordination.Status.DeploymentStates, activeDeploymentKey, coordination.Status.ActiveDeployment)
				// Update status and requeue to track the rollout
				r.updateConditions(&coordination, activeDeploymentKey, r.hasPendingRollouts(deploymentMap, activeDeploymentKey), false)
				coordination.Status.ActiveDeployment = activeDeploymentKey
				coordination.Status.Deployments = deploymentKeys
				coordination.Status.DeploymentStates = deploymentStates
				if err := r.updateStatusWithRetry(ctx, &coordination, logger); err != nil {
					logger.Error(err, "unable to update DeploymentCoordination status")
					return ctrl.Result{}, err
				}
				logger.Info("requeuing to track active deployment rollout", "deployment", key)
				return ctrl.Result{RequeueAfter: time.Second * 10}, nil
			}
		}

		// Find the first deployment that needs rollout (in sorted order for determinism)
		// Skip the deployment that just finished rolling out to avoid immediately reactivating it
		for _, key := range sortedKeys {
			// Skip the deployment that just finished (if any)
			if key == previousActiveDeploymentKey {
				logger.V(1).Info("skipping deployment that just finished rolling out", "deployment", key)
				continue
			}

			deployment := deploymentMap[key]
			needsRollout := r.needsRollout(deployment)

			if needsRollout && deployment.Spec.Paused {
				logger.Info("activating deployment (has pending changes)", "deployment", key)
				// Set active deployment key first
				activeDeploymentKey = key
				// Update status BEFORE unpausing to prevent webhook from pausing it again
				// Recalculate deployment states before updating status
				deploymentStates = r.recalculateDeploymentStates(sortedKeys, deploymentMap, coordination.Status.DeploymentStates, activeDeploymentKey, coordination.Status.ActiveDeployment)
				r.updateConditions(&coordination, activeDeploymentKey, r.hasPendingRollouts(deploymentMap, activeDeploymentKey), false)
				coordination.Status.ActiveDeployment = activeDeploymentKey
				coordination.Status.Deployments = deploymentKeys
				coordination.Status.DeploymentStates = deploymentStates
				if err := r.updateStatusWithRetry(ctx, &coordination, logger); err != nil {
					logger.Error(err, "unable to update DeploymentCoordination status before activating deployment")
					// Reset activeDeploymentKey on error
					activeDeploymentKey = ""
					return ctrl.Result{}, err
				}
				// Now that status is updated, unpause the deployment
				// The webhook will see it as active and won't pause it
				if err := r.updateDeploymentPaused(ctx, key, false, logger); err != nil {
					logger.Error(err, "unable to unpause deployment", "deployment", key)
					r.Recorder.Eventf(&coordination, corev1.EventTypeWarning, "ActivationFailed",
						"Failed to activate deployment %s: %v", key, err)
					r.updateConditions(&coordination, activeDeploymentKey, r.hasPendingRollouts(deploymentMap, activeDeploymentKey), true)
					if statusErr := r.updateStatusWithRetry(ctx, &coordination, logger); statusErr != nil {
						logger.Error(statusErr, "unable to update DeploymentCoordination status after activation failure")
					}
					return ctrl.Result{}, err
				}
				// Refresh deployment in map after update
				if err := r.Get(ctx, types.NamespacedName{Namespace: deployment.Namespace, Name: deployment.Name}, deployment); err == nil {
					deploymentMap[key] = deployment
				}
				r.Recorder.Eventf(&coordination, corev1.EventTypeNormal, "DeploymentActivated",
					"Activated deployment %s for rollout", key)
				logger.Info("requeuing to track active deployment rollout", "deployment", key)
				return ctrl.Result{RequeueAfter: time.Second * 10}, nil
			}
		}
	}

	// Recalculate deployment states right before updating status to ensure accuracy
	// This is important because deployments may have been updated during the reconcile loop
	oldActiveDeploymentKey := coordination.Status.ActiveDeployment
	// Use previousActiveDeploymentKey if we just cleared an active deployment, otherwise use the old one from status
	previousActiveForStates := previousActiveDeploymentKey
	if previousActiveForStates == "" {
		previousActiveForStates = oldActiveDeploymentKey
	}
	deploymentStates = r.recalculateDeploymentStates(sortedKeys, deploymentMap, coordination.Status.DeploymentStates, activeDeploymentKey, previousActiveForStates)

	// Check if there are still deployments that need rollout or are rolling out
	// This ensures we continue reconciling until all deployments are finished
	hasPendingRollouts := false
	for key, deployment := range deploymentMap {
		// Skip the active deployment (it's already being tracked)
		if key == activeDeploymentKey {
			continue
		}
		if r.needsRollout(deployment) || r.isRollingOut(deployment) {
			hasPendingRollouts = true
			break
		}
	}

	// Initialize conditions slice if nil
	if coordination.Status.Conditions == nil {
		coordination.Status.Conditions = []metav1.Condition{}
	}

	// Update conditions based on current state
	r.updateConditions(&coordination, activeDeploymentKey, hasPendingRollouts, false)

	// Update status
	coordination.Status.ActiveDeployment = activeDeploymentKey
	coordination.Status.Deployments = deploymentKeys
	coordination.Status.DeploymentStates = deploymentStates
	if err := r.updateStatusWithRetry(ctx, &coordination, logger); err != nil {
		logger.Error(err, "unable to update DeploymentCoordination status")
		r.Recorder.Eventf(&coordination, corev1.EventTypeWarning, "StatusUpdateFailed",
			"Failed to update DeploymentCoordination status: %v", err)
		// Update degraded condition on error
		r.updateConditions(&coordination, activeDeploymentKey, hasPendingRollouts, true)
		// Try to update status one more time with error condition
		_ = r.updateStatusWithRetry(ctx, &coordination, logger)
		return ctrl.Result{}, err
	}

	// Record event if active deployment changed
	if oldActiveDeploymentKey != activeDeploymentKey {
		if activeDeploymentKey != "" {
			r.Recorder.Eventf(&coordination, corev1.EventTypeNormal, "ActiveDeploymentChanged", "Active deployment changed from %s to %s", oldActiveDeploymentKey, activeDeploymentKey)
		} else if oldActiveDeploymentKey != "" {
			r.Recorder.Eventf(&coordination, corev1.EventTypeNormal, "ActiveDeploymentCleared", "Active deployment %s cleared (no active deployment)", oldActiveDeploymentKey)
		}
	}

	// If we have an active deployment or pending rollouts, requeue to continue coordination
	if activeDeploymentKey != "" || hasPendingRollouts {
		logger.V(1).Info("requeuing to continue coordination", "activeDeployment", activeDeploymentKey, "hasPendingRollouts", hasPendingRollouts)
		return ctrl.Result{RequeueAfter: time.Second * 10}, nil
	}

	// All deployments have finished rolling out
	logger.Info("all deployments have finished rolling out", "coordination", coordination.Name)
	return ctrl.Result{}, nil
}

// recalculateDeploymentStates recalculates the deployment states based on the current state of deployments.
// This ensures that hasPendingChanges is always accurate, especially for paused deployments with new specs.
// It also tracks rollout timestamps (started/finished) by comparing current state with previous state.
func (r *DeploymentCoordinationReconciler) recalculateDeploymentStates(sortedKeys []string, deploymentMap map[string]*appsv1k8s.Deployment, previousStates []appsv1.DeploymentState, activeDeploymentKey string, previousActiveDeploymentKey string) []appsv1.DeploymentState {
	// Build a map of previous states for quick lookup
	previousStateMap := make(map[string]appsv1.DeploymentState)
	for _, state := range previousStates {
		previousStateMap[state.Name] = state
	}

	deploymentStates := make([]appsv1.DeploymentState, 0, len(deploymentMap))
	now := metav1.Now()

	for _, key := range sortedKeys {
		deployment := deploymentMap[key]
		// Recalculate needsRollout to get the current state
		needsRollout := r.needsRollout(deployment)
		isRollingOut := r.isRollingOut(deployment)

		// Get previous state if it exists
		previousState, hadPreviousState := previousStateMap[key]

		// Determine if deployment was rolling out before by checking:
		// 1. If it had LastRolloutStarted set and LastRolloutFinished was nil or before LastRolloutStarted
		//    (meaning the rollout started but hasn't finished yet, or finished before it started which shouldn't happen)
		// 2. OR if it was the active deployment in the previous state
		wasRollingOut := false
		if hadPreviousState {
			// Check if it was rolling out based on timestamps
			// If LastRolloutStarted exists and LastRolloutFinished is nil or before LastRolloutStarted,
			// it means the rollout was in progress
			wasRollingOut = previousState.LastRolloutStarted != nil &&
				(previousState.LastRolloutFinished == nil ||
					previousState.LastRolloutFinished.Before(previousState.LastRolloutStarted))
		}
		// Also check if it was the previous active deployment (helps catch cases where timestamps weren't set)
		if !wasRollingOut && key == previousActiveDeploymentKey {
			wasRollingOut = true
		}

		// Also consider if this deployment is the active one - it should be considered as rolling out
		// even if replica counts haven't changed yet
		isActiveDeployment := key == activeDeploymentKey
		shouldBeRollingOut := isRollingOut || (isActiveDeployment && needsRollout)

		// If it was the active deployment before, it was likely rolling out
		// This helps catch cases where timestamps weren't set initially
		// Note: The case where isActiveDeployment && !isRollingOut && needsRollout
		// (just became active) is handled in the logic below

		state := appsv1.DeploymentState{
			Name:              key,
			HasPendingChanges: needsRollout,
			Generation:        deployment.Generation,
		}

		// Track rollout timestamps based on state transitions
		if shouldBeRollingOut {
			// Deployment is currently rolling out (or should be considered as such)
			if wasRollingOut {
				// Was already rolling out - preserve the start time
				if hadPreviousState && previousState.LastRolloutStarted != nil {
					state.LastRolloutStarted = previousState.LastRolloutStarted
				}
			} else {
				// Just started rolling out - set new start time
				state.LastRolloutStarted = &now
			}
			// Preserve finish time from previous completed rollout (if any) - it's historical data
			if hadPreviousState && previousState.LastRolloutFinished != nil {
				state.LastRolloutFinished = previousState.LastRolloutFinished
			}
		} else {
			// Deployment is not rolling out
			if wasRollingOut {
				// Just finished rolling out - set finish time
				state.LastRolloutFinished = &now
				// Preserve start time as historical information
				if hadPreviousState && previousState.LastRolloutStarted != nil {
					state.LastRolloutStarted = previousState.LastRolloutStarted
				}
			} else {
				// Was not rolling out - preserve existing timestamps as historical information
				if hadPreviousState {
					if previousState.LastRolloutStarted != nil {
						state.LastRolloutStarted = previousState.LastRolloutStarted
					}
					if previousState.LastRolloutFinished != nil {
						state.LastRolloutFinished = previousState.LastRolloutFinished
					}
				}
			}
		}

		deploymentStates = append(deploymentStates, state)
	}
	return deploymentStates
}

// hasPendingRollouts checks if there are any deployments that still need rollout or are currently rolling out,
// excluding the optionally provided activeDeploymentKey.
func (r *DeploymentCoordinationReconciler) hasPendingRollouts(deploymentMap map[string]*appsv1k8s.Deployment, activeDeploymentKey string) bool {
	for key, deployment := range deploymentMap {
		if key == activeDeploymentKey {
			continue
		}
		if r.needsRollout(deployment) || r.isRollingOut(deployment) {
			return true
		}
	}
	return false
}

// SetupWithManager sets up the controller with the Manager.
func (r *DeploymentCoordinationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// Initialize event recorder if not already set
	if r.Recorder == nil {
		r.Recorder = mgr.GetEventRecorderFor("deploymentcoordination-controller")
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&appsv1.DeploymentCoordination{}).
		Watches(&appsv1k8s.Deployment{}, handler.EnqueueRequestsFromMapFunc(
			func(ctx context.Context, obj client.Object) []reconcile.Request {
				deployment := obj.(*appsv1k8s.Deployment)
				// Find all DeploymentCoordination resources that might match this deployment
				// and enqueue them for reconciliation
				var coordinations appsv1.DeploymentCoordinationList
				if err := mgr.GetClient().List(ctx, &coordinations, client.InNamespace(deployment.Namespace)); err != nil {
					return nil
				}

				requests := make([]reconcile.Request, 0)
				deploymentLabels := labels.Set(deployment.GetLabels())

				for i := range coordinations.Items {
					coordination := &coordinations.Items[i]
					selector, err := metav1.LabelSelectorAsSelector(&coordination.Spec.LabelSelector)
					if err != nil {
						continue
					}
					if selector.Matches(deploymentLabels) {
						requests = append(requests, reconcile.Request{
							NamespacedName: types.NamespacedName{
								Name:      coordination.Name,
								Namespace: coordination.Namespace,
							},
						})
					}
				}

				return requests
			})).
		Named("deploymentcoordination").
		WithOptions(controller.Options{
			// MaxConcurrentReconciles: r.MaxConcurrentReconciles,
			RateLimiter: workqueue.NewTypedItemFastSlowRateLimiter[reconcile.Request](1*time.Second, 20*time.Second, 15),
		}).
		WithEventFilter(predicate.Funcs{
			CreateFunc: func(e event.CreateEvent) bool {
				return true
			},
			DeleteFunc: func(e event.DeleteEvent) bool {
				// The reconciler adds a finalizer so we perform clean-up
				// when the delete timestamp is added
				// Suppress Delete events to avoid filtering them out in the Reconcile function
				return false
			},
			UpdateFunc: func(e event.UpdateEvent) bool {
				// if _, ok := e.ObjectNew.(*appsv1.Deployment); ok {
				// 	return true
				// }
				oldGeneration := e.ObjectOld.GetGeneration()
				newGeneration := e.ObjectNew.GetGeneration()
				// Generation is only updated on spec changes (also on deletion), not metadata or status
				// Filter out events where the generation hasn't changed to avoid being triggered by status updates
				return oldGeneration != newGeneration
			},
			GenericFunc: func(e event.GenericEvent) bool {
				return false
			},
		}).
		Complete(r)
}

// isRollingOut checks if a deployment is currently rolling out.
// A deployment is rolling out if it has replicas that are not yet in the desired state.
// This is based on replica counts, not generation, because observedGeneration can be
// updated before the rollout actually completes.
func (r *DeploymentCoordinationReconciler) isRollingOut(deployment *appsv1k8s.Deployment) bool {
	// If paused, it's not rolling out (even if replica counts are wrong)
	if deployment.Spec.Paused {
		return false
	}

	desiredReplicas := int32(1)
	if deployment.Spec.Replicas != nil {
		desiredReplicas = *deployment.Spec.Replicas
	}

	// Deployment is rolling out if replica counts don't match desired state:
	// - Updated replicas are less than desired (still updating pods)
	// - Ready replicas are less than desired (not all pods ready)
	// - Available replicas are less than desired (not all pods available)
	// - Replicas count doesn't match (scaling in progress)
	if deployment.Status.Replicas != desiredReplicas ||
		deployment.Status.UpdatedReplicas < desiredReplicas ||
		deployment.Status.ReadyReplicas < desiredReplicas ||
		deployment.Status.AvailableReplicas < desiredReplicas {
		return true
	}

	// Also check if there's a new spec that hasn't been observed yet
	// This catches the case where a new spec was just applied
	if deployment.Status.ObservedGeneration < deployment.Generation {
		return true
	}

	return false
}

// needsRollout checks if a deployment needs to roll out.
// A deployment needs rollout if it has pending changes (generation mismatch) or
// if it's not in the desired state based on replica counts.
// Note: We prioritize replica counts over generation because observedGeneration
// can be updated before the rollout actually completes.
func (r *DeploymentCoordinationReconciler) needsRollout(deployment *appsv1k8s.Deployment) bool {
	desiredReplicas := int32(1)
	if deployment.Spec.Replicas != nil {
		desiredReplicas = *deployment.Spec.Replicas
	}

	// Primary check: Are replicas not in desired state?
	// This is the most reliable indicator that a rollout is needed or in progress
	if deployment.Status.Replicas != desiredReplicas ||
		deployment.Status.UpdatedReplicas != desiredReplicas ||
		deployment.Status.ReadyReplicas != desiredReplicas ||
		deployment.Status.AvailableReplicas != desiredReplicas {
		return true
	}

	// Secondary check: If generation doesn't match observed generation, there are pending changes
	// This catches cases where a new spec was just applied but replica counts haven't changed yet
	if deployment.Status.ObservedGeneration < deployment.Generation {
		return true
	}

	return false
}

// getDeploymentKeyForCoordination returns a unique key for a deployment (namespace/name).
func getDeploymentKeyForCoordination(deployment *appsv1k8s.Deployment) string {
	return fmt.Sprintf("%s/%s", deployment.Namespace, deployment.Name)
}

// isReady checks if a deployment is ready (all replicas are available and ready).
func (r *DeploymentCoordinationReconciler) isReady(deployment *appsv1k8s.Deployment) bool {
	desiredReplicas := int32(1)
	if deployment.Spec.Replicas != nil {
		desiredReplicas = *deployment.Spec.Replicas
	}

	// Deployment is ready when:
	// - All replicas are available
	// - All replicas are ready
	// - Generation matches observed generation (no pending changes)
	return deployment.Status.AvailableReplicas == desiredReplicas &&
		deployment.Status.ReadyReplicas == desiredReplicas &&
		deployment.Status.ObservedGeneration >= deployment.Generation
}

// getReadyTime returns the time when the deployment became ready.
// It checks the Available condition's last transition time.
func (r *DeploymentCoordinationReconciler) getReadyTime(deployment *appsv1k8s.Deployment) time.Time {
	if !r.isReady(deployment) {
		return time.Time{}
	}

	// Find the Available condition
	for _, condition := range deployment.Status.Conditions {
		if condition.Type == appsv1k8s.DeploymentAvailable && condition.Status == "True" {
			return condition.LastTransitionTime.Time
		}
	}

	// If no Available condition but deployment is ready, use current time as fallback
	// This shouldn't happen in practice, but provides a safe default
	if r.isReady(deployment) {
		return time.Now()
	}

	return time.Time{}
}

// hasBeenReadyForMinTime checks if a deployment has been ready for at least minReadySeconds.
func (r *DeploymentCoordinationReconciler) hasBeenReadyForMinTime(deployment *appsv1k8s.Deployment, minReadySeconds int32) bool {
	if !r.isReady(deployment) {
		return false
	}

	if minReadySeconds <= 0 {
		return true
	}

	readyTime := r.getReadyTime(deployment)
	if readyTime.IsZero() {
		return false
	}

	elapsed := time.Since(readyTime)
	return elapsed >= time.Duration(minReadySeconds)*time.Second
}

// setCondition sets a condition on the DeploymentCoordination status.
func (r *DeploymentCoordinationReconciler) setCondition(coordination *appsv1.DeploymentCoordination, conditionType string, status metav1.ConditionStatus, reason, message string) {
	now := metav1.Now()
	condition := metav1.Condition{
		Type:               conditionType,
		Status:             status,
		Reason:             reason,
		Message:            message,
		LastTransitionTime: now,
		ObservedGeneration: coordination.Generation,
	}

	// Find existing condition
	existingIdx := -1
	for i, c := range coordination.Status.Conditions {
		if c.Type == conditionType {
			existingIdx = i
			break
		}
	}

	if existingIdx >= 0 {
		existing := coordination.Status.Conditions[existingIdx]
		// Only update if status or reason changed
		if existing.Status == status && existing.Reason == reason && existing.Message == message {
			// No change needed, just update observed generation
			coordination.Status.Conditions[existingIdx].ObservedGeneration = coordination.Generation
			return
		}
		// Update existing condition
		condition.LastTransitionTime = existing.LastTransitionTime
		if existing.Status != status {
			condition.LastTransitionTime = now
		}
		coordination.Status.Conditions[existingIdx] = condition
	} else {
		// Add new condition
		coordination.Status.Conditions = append(coordination.Status.Conditions, condition)
	}
}

// updateConditions updates all conditions based on the current state of the coordination.
func (r *DeploymentCoordinationReconciler) updateConditions(coordination *appsv1.DeploymentCoordination, activeDeploymentKey string, hasPendingRollouts bool, hasError bool) {
	// Update Ready condition
	if hasError {
		r.setCondition(coordination, appsv1.ConditionTypeReady, metav1.ConditionFalse, "Error", "Coordination has errors")
	} else if activeDeploymentKey == "" && !hasPendingRollouts {
		r.setCondition(coordination, appsv1.ConditionTypeReady, metav1.ConditionTrue, "AllDeploymentsReady", "All coordinated deployments have finished rolling out")
	} else {
		r.setCondition(coordination, appsv1.ConditionTypeReady, metav1.ConditionFalse, "DeploymentsInProgress", "One or more deployments are still rolling out")
	}

	// Update Progressing condition
	if hasError {
		r.setCondition(coordination, appsv1.ConditionTypeProgressing, metav1.ConditionFalse, "Error", "Coordination has errors")
	} else if activeDeploymentKey != "" {
		r.setCondition(coordination, appsv1.ConditionTypeProgressing, metav1.ConditionTrue, "DeploymentRollingOut", fmt.Sprintf("Deployment %s is rolling out", activeDeploymentKey))
	} else if hasPendingRollouts {
		r.setCondition(coordination, appsv1.ConditionTypeProgressing, metav1.ConditionTrue, "DeploymentsPending", "Deployments are waiting to roll out")
	} else {
		r.setCondition(coordination, appsv1.ConditionTypeProgressing, metav1.ConditionFalse, "AllDeploymentsReady", "All coordinated deployments have finished rolling out")
	}

	// Update Degraded condition
	if hasError {
		r.setCondition(coordination, appsv1.ConditionTypeDegraded, metav1.ConditionTrue, "Error", "Coordination has errors")
	} else {
		r.setCondition(coordination, appsv1.ConditionTypeDegraded, metav1.ConditionFalse, "NoErrors", "Coordination is functioning normally")
	}
}

// updateDeploymentPaused updates a deployment's paused state with retry logic.
// It handles conflict errors by fetching the latest version and retrying the update.
// Returns an error if all retries are exhausted.
func (r *DeploymentCoordinationReconciler) updateDeploymentPaused(ctx context.Context, deploymentKey string, paused bool, logger logr.Logger) error {
	const maxRetries = 5
	const baseDelay = 100 * time.Millisecond

	// Parse deployment key (namespace/name)
	parts := strings.Split(deploymentKey, "/")
	if len(parts) != 2 {
		return fmt.Errorf("invalid deployment key format: %s (expected namespace/name)", deploymentKey)
	}
	namespace, name := parts[0], parts[1]

	for attempt := 0; attempt < maxRetries; attempt++ {
		// Fetch the latest version of the deployment
		var deployment appsv1k8s.Deployment
		if err := r.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, &deployment); err != nil {
			return fmt.Errorf("failed to fetch deployment: %w", err)
		}

		// Check if the state is already what we want
		if deployment.Spec.Paused == paused {
			return nil
		}

		// Apply the change
		deployment.Spec.Paused = paused

		// Try to update
		err := r.Update(ctx, &deployment)
		if err == nil {
			return nil
		}

		// If it's a conflict error, retry
		if errors.IsConflict(err) {
			if attempt < maxRetries-1 {
				// Exponential backoff
				delay := baseDelay * time.Duration(1<<uint(attempt))
				logger.V(1).Info("deployment update conflict, retrying", "deployment", deploymentKey, "attempt", attempt+1, "delay", delay)
				time.Sleep(delay)
				continue
			}
		}

		// For non-conflict errors or if we've exhausted retries, return the error
		return err
	}

	return fmt.Errorf("failed to update deployment %s after %d attempts", deploymentKey, maxRetries)
}

// updateStatusWithRetry updates the DeploymentCoordination status with retry logic.
// It handles conflict errors by fetching the latest version and retrying the update.
// Returns an error if all retries are exhausted.
func (r *DeploymentCoordinationReconciler) updateStatusWithRetry(ctx context.Context, coordination *appsv1.DeploymentCoordination, logger logr.Logger) error {
	const maxRetries = 5
	const baseDelay = 100 * time.Millisecond

	for attempt := 0; attempt < maxRetries; attempt++ {
		err := r.Status().Update(ctx, coordination)
		if err == nil {
			return nil
		}

		// If it's a conflict error, fetch the latest version and retry
		if errors.IsConflict(err) {
			if attempt < maxRetries-1 {
				// Fetch the latest version
				latest := &appsv1.DeploymentCoordination{}
				if getErr := r.Get(ctx, types.NamespacedName{
					Name:      coordination.Name,
					Namespace: coordination.Namespace,
				}, latest); getErr != nil {
					logger.Error(getErr, "failed to fetch latest DeploymentCoordination for retry", "attempt", attempt+1)
					return fmt.Errorf("failed to fetch latest version for retry: %w", getErr)
				}

				// Update the status fields on the latest version
				latest.Status.ActiveDeployment = coordination.Status.ActiveDeployment
				latest.Status.Deployments = coordination.Status.Deployments
				latest.Status.DeploymentStates = coordination.Status.DeploymentStates
				latest.Status.Conditions = coordination.Status.Conditions
				coordination = latest

				// Exponential backoff
				delay := baseDelay * time.Duration(1<<uint(attempt))
				logger.V(1).Info("status update conflict, retrying", "attempt", attempt+1, "delay", delay)
				time.Sleep(delay)
				continue
			}
		}

		// For non-conflict errors or if we've exhausted retries, return the error
		return err
	}

	return fmt.Errorf("failed to update status after %d attempts", maxRetries)
}
