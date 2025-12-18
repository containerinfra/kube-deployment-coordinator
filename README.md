# kube-deployment-coordinator

A Kubernetes controller that coordinates the rollout of multiple deployments to ensure only one deployment rolls out at a time. This prevents resource contention and ensures predictable, sequential rollouts across related deployments.

> **Note:** This project targets a niche coordination problem in Kubernetes: ensuring _sequential rollouts_ across multiple Deployments that _should not_ be updated in parallel. 
> 
> Most users of Kubernetes do **not** need this—native Deployments already support rolling updates, readiness checks, and gradual rollout. If you are not explicitly running multiple Deployments for the same app (for example, with different network or node configurations) and struggling with them being updated at the same time (usually by GitOps tools), you are likely solving the wrong problem, or employing an anti-pattern in your architecture.  
> 
> If this situation fits your use-case, you'll know; otherwise, consider revisiting your deployment strategy before adopting this solution.

## Overview

The `kube-deployment-coordinator` is a Kubernetes operator that manages the coordinated rollout of deployments using a Custom Resource Definition (CRD). It ensures that deployments matching a label selector roll out one at a time, preventing simultaneous rollouts that could cause resource contention or service disruption.

### Key Features

- Rolls out deployments one at a time
- Coordinates deployments using label selectors
- Waits for deployments to be ready before continuing
- Tracks rollout progress and status
- Pauses non-active deployments automatically
- Records Kubernetes events

## Use Cases

### Multiple Deployments with Different Pod Configurations

When you need to run the same application with different pod configurations (e.g., different network annotations for Multus CNI, different node selectors, or different resource requirements), you typically create separate deployments. However, during upgrades, you want to maintain high availability by ensuring only one deployment rolls out at a time.

**Example Scenario:**
- Deployment A: Uses static IP annotation for Pod network interface (e.g., `k8s.v1.cni.cncf.io/networks` with a specific static IP)
- Deployment B: Uses a different static IP annotation or network interface for its Pods
- Both deployments run the same application but require different network configurations
- During upgrades, you want to ensure at least one deployment is always available

When using GitOps tools (like FluxCD or ArgoCD) combined with automated dependency updates (like Renovate), multiple deployments can receive updates simultaneously. This can cause downtime for the application.

The `kube-deployment-coordinator` safeguards that when Deployment A is rolling out, Deployment B remains stable, and vice versa, maintaining service availability.

### Alternative Solutions

Other alternatives for coordinating sequential rollouts often involve adopting more complex or heavyweight deployment platforms, such as Argo Rollouts or Spinnaker. These platforms provide advanced deployment strategies and rollout controls, but can introduce significant operational overhead and may require re-architecting existing workflows. 

This approach conflicts with organizations that have already established CI/CD pipelines using tools like FluxCD, ArgoCD, or native Kubernetes deployments, and want to avoid introducing a separate toolchain or controller solely for sequential deployment coordination. In these cases, `kube-deployment-coordinator` offers a lightweight, Kubernetes-native solution focused specifically on this use case with minimal disruption to existing processes.

## How It Works

1. **Webhook**: A mutating webhook automatically pauses deployments that match a `DeploymentCoordination` resource but are not the active deployment
2. **Controller**: The controller manages the coordination by:
   - Identifying deployments that match the label selector
   - Activating one deployment at a time (unpausing it)
   - Tracking rollout progress using replica counts
   - Waiting for `MinReadySeconds` after a deployment completes before activating the next one
   - Updating status with rollout timestamps and deployment states

## Installation

### Using Helm (FluxCD)

```yaml
---
apiVersion: source.toolkit.fluxcd.io/v1
kind: OCIRepository
metadata:
  name: kube-deployment-coordinator-crds
  namespace: kube-system
spec:
  interval: 160m
  url: oci://ghcr.io/containerinfra/charts/kube-deployment-coordinator-crds
  ref:
    semver: ">= 0.0.0"
---
apiVersion: helm.toolkit.fluxcd.io/v2
kind: HelmRelease
metadata:
  name: kube-deployment-coordinator-crds
  namespace: kube-system
spec:
  chartRef:
    kind: OCIRepository
    name: kube-deployment-coordinator-crds
    namespace: kube-system
  interval: 1h
  values: {}
---
apiVersion: source.toolkit.fluxcd.io/v1
kind: OCIRepository
metadata:
  name: kube-deployment-coordinator
  namespace: kube-system
spec:
  interval: 160m
  url: oci://ghcr.io/containerinfra/charts/kube-deployment-coordinator
  ref:
    semver: ">= 0.0.0"
---
apiVersion: helm.toolkit.fluxcd.io/v2
kind: HelmRelease
metadata:
  name: kube-deployment-coordinator
  namespace: kube-system
spec:
  chartRef:
    kind: OCIRepository
    name: kube-deployment-coordinator
    namespace: kube-system
  interval: 1h
  install:
    crds: Skip
  values:
    enableServiceMonitor: false
    replicaCount: 1
    affinity:
      nodeAffinity:
        preferredDuringSchedulingIgnoredDuringExecution:
          - weight: 1
            preference:
              matchExpressions:
                - key: node-role.kubernetes.io/control-plane
                  operator: Exists
    nodeSelector:
      kubernetes.io/os: linux
    tolerations:
      - key: node-role.kubernetes.io/control-plane
        effect: NoSchedule
```

## Usage

### 1. Create a DeploymentCoordination Resource

Define which deployments should be coordinated using a label selector:

```yaml
apiVersion: apps.containerinfra.nl/v1
kind: DeploymentCoordination
metadata:
  name: nginx-coordination
  namespace: default
spec:
  labelSelector:
    matchLabels:
      component: nginx
  minReadySeconds: 10
```

**Fields:**
- `labelSelector`: Kubernetes label selector to match deployments that should be coordinated
- `minReadySeconds`: (Optional) Minimum number of seconds the active deployment must be ready before the next one can start. Defaults to 0.

### 2. Label Your Deployments

Ensure your deployments have labels that match the selector:

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: deployment-1
  labels:
    component: nginx  # Matches the labelSelector above
spec:
  replicas: 1
  # ... rest of deployment spec
```

### 3. How Coordination Works

Once you have a `DeploymentCoordination` resource and matching deployments:

1. **Initial State**: All matching deployments are automatically paused by the webhook or controller
2. **Activation**: The controller activates the first deployment (in alphabetical order by namespace/name)
3. **Rollout**: The active deployment rolls out while others remain paused
4. **Completion**: Once the active deployment finishes rolling out and has been ready for `MinReadySeconds`, it's cleared
5. **Next Deployment**: The next deployment with pending changes is activated
6. **Repeat**: This continues until all deployments have finished rolling out

### 4. Checking Status

View the coordination status:

```bash
kubectl get deploymentcoordination nginx-coordination -o yaml
```

The status includes:
- `activeDeployment`: The currently active deployment (namespace/name format)
- `deployments`: List of all coordinated deployments
- `deploymentStates`: Detailed state for each deployment including:
  - `hasPendingChanges`: Whether the deployment needs rollout
  - `lastRolloutStarted`: Timestamp when the last rollout started
  - `lastRolloutFinished`: Timestamp when the last rollout finished
- `conditions`: Standard Kubernetes conditions (Ready, Progressing, Degraded)

Example status output:

```yaml
status:
  activeDeployment: default/deployment-1
  deployments:
    - default/deployment-1
    - default/deployment-2
    - default/deployment-3
  deploymentStates:
    - name: default/deployment-1
      hasPendingChanges: false
      generation: 2
      lastRolloutStarted: "2025-01-20T10:00:00Z"
      lastRolloutFinished: "2025-01-20T10:05:00Z"
    - name: default/deployment-2
      hasPendingChanges: true
      generation: 1
  conditions:
    - type: Ready
      status: "False"
      reason: DeploymentsInProgress
    - type: Progressing
      status: "True"
      reason: DeploymentRollingOut
```

### 5. Viewing Events

Monitor coordination events:

```bash
kubectl get events --field-selector involvedObject.name=nginx-coordination --sort-by='.lastTimestamp'
```

Common events:
- `DeploymentActivated`: A deployment was activated for rollout
- `DeploymentPaused`: A deployment was paused
- `DeploymentUnpaused`: A deployment was unpaused
- `ActiveDeploymentReady`: The active deployment finished rolling out
- `ActiveDeploymentCleared`: The active deployment was cleared
- `ActiveDeploymentChanged`: The active deployment changed

## Examples

### Example: Coordinating Multiple Nginx Deployments

```yaml
# 1. Create the coordination resource
apiVersion: apps.containerinfra.nl/v1
kind: DeploymentCoordination
metadata:
  name: nginx
  namespace: default
spec:
  labelSelector:
    matchLabels:
      component: nginx
  minReadySeconds: 30

---
# 2. Create multiple deployments with matching labels
apiVersion: apps/v1
kind: Deployment
metadata:
  name: nginx-frontend
  labels:
    component: nginx
spec:
  replicas: 3
  template:
    metadata:
      labels:
        app: nginx-frontend
    spec:
      containers:
      - name: nginx
        image: nginx:1.21

---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: nginx-backend
  labels:
    component: nginx
spec:
  replicas: 2
  template:
    metadata:
      labels:
        app: nginx-backend
    spec:
      containers:
      - name: nginx
        image: nginx:1.21
```

When you update the image in `nginx-frontend`, it will roll out first. Once it completes and has been ready for 30 seconds, `nginx-backend` will automatically start rolling out if it has pending changes.

## Troubleshooting

### Deployments Not Being Coordinated

1. **Check label selector match**: Ensure your deployments have labels that match the `labelSelector` in the `DeploymentCoordination` resource
2. **Verify webhook is working**: Check if deployments are being paused automatically
3. **Check controller logs**: 
   ```bash
   kubectl logs -n kube-deployment-coordinator-system deployment/kube-deployment-coordinator-controller-manager
   ```

### Deployment Stuck

1. **Check if deployment is paused**: `kubectl get deployment <name> -o jsonpath='{.spec.paused}'`
2. **Check coordination status**: `kubectl get deploymentcoordination <name> -o yaml`
3. **Verify active deployment**: Check if another deployment is currently active
4. **Check events**: Look for error events on the `DeploymentCoordination` resource

### Multiple Deployments Rolling Out Simultaneously

This should not happen, but if it does:
1. Check if multiple `DeploymentCoordination` resources match the same deployments
2. Verify the webhook is enabled and functioning
3. Check controller logs for errors

## Development

### Building

```bash
make build
```

### Testing

```bash
# Run unit tests
make test

# Run e2e tests (requires Kind cluster)
make test-e2e
```

### Running Locally

```bash
# Generate webhook certificates (if needed)
./hack/generate-webhook-certs.sh

# Run the controller
go run cmd/main.go --webhook-cert-dir=/tmp/k8s-webhook-server/serving-certs
```

## License

Copyright 2025 ContainerInfra.

Licensed under the Apache License, Version 2.0.
