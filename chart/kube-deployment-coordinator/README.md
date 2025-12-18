# kube-deployment-coordinator

![Version: 1.0.0](https://img.shields.io/badge/Version-1.0.0-informational?style=flat-square) ![Type: application](https://img.shields.io/badge/Type-application-informational?style=flat-square) ![AppVersion: 1.0.0](https://img.shields.io/badge/AppVersion-1.0.0-informational?style=flat-square)

A Kubernetes controller that coordinates the rollout of multiple deployments to ensure only one deployment rolls out at a time. This Helm chart deploys the controller, mutating webhook, and required RBAC resources.

## Description

The `kube-deployment-coordinator` is a Kubernetes operator that manages sequential rollouts of deployments using a Custom Resource Definition (CRD). It ensures deployments matching a label selector roll out one at a time, preventing simultaneous rollouts that could cause resource contention or service disruption.

This chart includes:
- Controller deployment for managing `DeploymentCoordination` resources
- Mutating webhook for automatically pausing non-active deployments
- Service account and RBAC permissions
- Optional ServiceMonitor for Prometheus metrics
- Optional cert-manager integration for webhook TLS certificates

## Source Code

* <https://github.com/containerinfra/kube-deployment-coordinator>

## Cert-Manager Integration

The chart supports using cert-manager for automatic TLS certificate management for the webhook. To enable cert-manager:

```yaml
webhook:
  enabled: true
  certManager:
    enabled: true
    # Create a self-signed issuer (default: true)
    createIssuer: true
    # Or use an existing issuer
    # createIssuer: false
    # issuerName: my-existing-issuer
    # Or use a ClusterIssuer
    # issuerRef:
    #   kind: ClusterIssuer
    #   name: letsencrypt-prod
```

When `certManager.enabled` is `true`:
- A `Certificate` resource will be created for the webhook
- An optional self-signed `Issuer` will be created if `createIssuer` is `true`
- The certificate secret will be automatically mounted in the controller pod
- The `MutatingWebhookConfiguration` will be annotated for automatic CA injection by cert-manager
- The `caBundle` field in the webhook configuration will be automatically populated

**Note:** Ensure cert-manager is installed in your cluster before enabling this feature.

## Values

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| affinity | object | `{}` |  |
| enableServiceMonitor | bool | `true` |  |
| envFrom | list | `[]` |  |
| extraArgs | list | `[]` |  |
| fullnameOverride | string | `""` |  |
| hpa.cpu.averageUtilization | int | `90` |  |
| hpa.cpu.targetType | string | `"Utilization"` |  |
| hpa.enabled | bool | `false` |  |
| hpa.maxReplicas | int | `4` |  |
| hpa.minReplicas | int | `2` |  |
| image.pullPolicy | string | `"IfNotPresent"` |  |
| image.repository | string | `"ghcr.io/containerinfra/kube-deployment-coordinator"` |  |
| image.tag | string | `nil` |  |
| imagePullSecrets | object | `{}` |  |
| leaderElection.enabled | bool | `false` |  |
| livenessProbe.failureThreshold | int | `3` |  |
| livenessProbe.httpGet.path | string | `"/healthz"` |  |
| livenessProbe.httpGet.port | string | `"health"` |  |
| livenessProbe.initialDelaySeconds | int | `10` |  |
| livenessProbe.periodSeconds | int | `10` |  |
| livenessProbe.timeoutSeconds | int | `10` |  |
| minAvailable | int | `1` |  |
| nameOverride | string | `""` |  |
| nodeSelector | object | `{}` |  |
| podAnnotations | object | `{}` |  |
| podSecurityContext.fsGroup | int | `65532` |  |
| podSecurityContext.runAsGroup | int | `65532` |  |
| podSecurityContext.runAsNonRoot | bool | `true` |  |
| podSecurityContext.runAsUser | int | `65532` |  |
| podSecurityContext.seccompProfile.type | string | `"RuntimeDefault"` |  |
| readinessProbe.failureThreshold | int | `3` |  |
| readinessProbe.httpGet.path | string | `"/readyz"` |  |
| readinessProbe.httpGet.port | string | `"health"` |  |
| readinessProbe.initialDelaySeconds | int | `10` |  |
| readinessProbe.periodSeconds | int | `10` |  |
| readinessProbe.timeoutSeconds | int | `10` |  |
| replicaCount | int | `1` |  |
| resources | object | `{}` |  |
| securityContext.allowPrivilegeEscalation | bool | `false` |  |
| securityContext.capabilities.drop[0] | string | `"ALL"` |  |
| securityContext.readOnlyRootFilesystem | bool | `true` |  |
| securityContext.runAsUser | int | `65532` |  |
| service.type | string | `"ClusterIP"` |  |
| serviceAccount.annotations | object | `{}` |  |
| serviceAccount.create | bool | `true` |  |
| serviceAccount.name | string | `""` |  |
| tolerations | list | `[]` |  |
| webhook.admissionReviewVersions[0] | string | `"v1"` |  |
| webhook.annotations | object | `{}` |  |
| webhook.caBundle | string | `""` |  |
| webhook.enabled | bool | `true` |  |
| webhook.failurePolicy | string | `"Ignore"` |  |
| webhook.name | string | `""` |  |
| webhook.namespaceSelector | object | `{}` |  |
| webhook.objectSelector | object | `{}` |  |
| webhook.operations[0] | string | `"CREATE"` |  |
| webhook.operations[1] | string | `"UPDATE"` |  |
| webhook.path | string | `"/mutate-apps-v1-deployment"` |  |
| webhook.serviceName | string | `"webhook-service"` |  |
| webhook.sideEffects | string | `"None"` |  |
| webhook.webhookName | string | `"kube-deployment-coordinator.containerinfra.nl"` |  |

----------------------------------------------
Autogenerated from chart metadata using [helm-docs v1.14.2](https://github.com/norwoodj/helm-docs/releases/v1.14.2)
