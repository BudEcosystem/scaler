# Scaler Helm Chart

Helm chart for deploying BudAIScaler - a Kubernetes autoscaler for GenAI workloads.

## Prerequisites

- Kubernetes 1.26+
- Helm 3.x

## Installation

```bash
# Install with default values
helm install scaler . \
  --namespace scaler-system \
  --create-namespace

# Install with custom values
helm install scaler . \
  --namespace scaler-system \
  --create-namespace \
  --set image.tag=v0.1.0 \
  --set gpu.enabled=true
```

## Uninstallation

```bash
helm uninstall scaler -n scaler-system
```

> **Note:** CRDs are not deleted by default. To remove them:
> ```bash
> kubectl delete crd budaiscalers.scaler.bud.studio
> ```

## Configuration

### General

| Parameter | Description | Default |
|-----------|-------------|---------|
| `replicaCount` | Number of controller replicas | `1` |
| `nameOverride` | Override chart name | `""` |
| `fullnameOverride` | Override full name | `""` |

### Image

| Parameter | Description | Default |
|-----------|-------------|---------|
| `image.repository` | Controller image repository | `ghcr.io/budecosystem/scaler` |
| `image.tag` | Image tag (defaults to appVersion) | `""` |
| `image.pullPolicy` | Image pull policy | `IfNotPresent` |
| `imagePullSecrets` | Image pull secrets | `[]` |

### Controller

| Parameter | Description | Default |
|-----------|-------------|---------|
| `controller.leaderElect` | Enable leader election | `true` |
| `controller.metricsBindAddress` | Metrics bind address | `:8080` |
| `controller.healthProbeBindAddress` | Health probe bind address | `:8081` |
| `controller.logLevel` | Log level | `info` |

### Resources

| Parameter | Description | Default |
|-----------|-------------|---------|
| `resources.limits.cpu` | CPU limit | `500m` |
| `resources.limits.memory` | Memory limit | `256Mi` |
| `resources.requests.cpu` | CPU request | `100m` |
| `resources.requests.memory` | Memory request | `128Mi` |

### Security

| Parameter | Description | Default |
|-----------|-------------|---------|
| `podSecurityContext.runAsNonRoot` | Run as non-root | `true` |
| `securityContext.allowPrivilegeEscalation` | Allow privilege escalation | `false` |
| `securityContext.readOnlyRootFilesystem` | Read-only root filesystem | `true` |

### RBAC

| Parameter | Description | Default |
|-----------|-------------|---------|
| `rbac.create` | Create ClusterRole and ClusterRoleBinding | `true` |
| `serviceAccount.create` | Create ServiceAccount | `true` |
| `serviceAccount.name` | ServiceAccount name | `""` |
| `serviceAccount.annotations` | ServiceAccount annotations | `{}` |

### CRDs

| Parameter | Description | Default |
|-----------|-------------|---------|
| `crds.install` | Install CRDs | `true` |
| `crds.keep` | Keep CRDs on uninstall | `true` |

### Metrics

| Parameter | Description | Default |
|-----------|-------------|---------|
| `metrics.enabled` | Enable Prometheus metrics | `true` |
| `metrics.service.type` | Metrics service type | `ClusterIP` |
| `metrics.service.port` | Metrics service port | `8080` |

### Prometheus Integration

| Parameter | Description | Default |
|-----------|-------------|---------|
| `prometheus.endpoint` | Default Prometheus endpoint | `""` |
| `prometheus.auth.enabled` | Enable Prometheus auth | `false` |
| `prometheus.auth.tokenSecretName` | Bearer token secret name | `""` |
| `prometheus.auth.tokenSecretKey` | Bearer token secret key | `token` |

### GPU

| Parameter | Description | Default |
|-----------|-------------|---------|
| `gpu.enabled` | Enable GPU metrics | `false` |
| `gpu.dcgmEndpoint` | DCGM exporter endpoint | `""` |

### Cost

| Parameter | Description | Default |
|-----------|-------------|---------|
| `cost.enabled` | Enable cost tracking | `false` |
| `cost.cloudProvider` | Cloud provider (aws, gcp, azure) | `""` |

### Multi-Cluster

| Parameter | Description | Default |
|-----------|-------------|---------|
| `multiCluster.enabled` | Enable multi-cluster | `false` |
| `multiCluster.clusters` | List of remote clusters | `[]` |

### Scheduling

| Parameter | Description | Default |
|-----------|-------------|---------|
| `nodeSelector` | Node selector | `{}` |
| `tolerations` | Tolerations | `[]` |
| `affinity` | Affinity rules | `{}` |

## Examples

### Basic Installation

```bash
helm install scaler . -n scaler-system --create-namespace
```

### With GPU Support

```bash
helm install scaler . -n scaler-system --create-namespace \
  --set gpu.enabled=true \
  --set gpu.dcgmEndpoint=http://dcgm-exporter:9400
```

### With Prometheus

```bash
helm install scaler . -n scaler-system --create-namespace \
  --set prometheus.endpoint=http://prometheus.monitoring:9090
```

### With Custom Resources

```bash
helm install scaler . -n scaler-system --create-namespace \
  --set resources.limits.cpu=1 \
  --set resources.limits.memory=512Mi \
  --set resources.requests.cpu=200m \
  --set resources.requests.memory=256Mi
```

### Production Configuration

```yaml
# values-production.yaml
replicaCount: 2

resources:
  limits:
    cpu: 1
    memory: 512Mi
  requests:
    cpu: 200m
    memory: 256Mi

affinity:
  podAntiAffinity:
    preferredDuringSchedulingIgnoredDuringExecution:
      - weight: 100
        podAffinityTerm:
          labelSelector:
            matchLabels:
              app.kubernetes.io/name: scaler
          topologyKey: kubernetes.io/hostname

metrics:
  enabled: true

gpu:
  enabled: true

prometheus:
  endpoint: http://prometheus.monitoring:9090
```

```bash
helm install scaler . -n scaler-system --create-namespace -f values-production.yaml
```

## Upgrading

```bash
helm upgrade scaler . -n scaler-system
```

## Troubleshooting

### Check Controller Logs

```bash
kubectl logs -n scaler-system -l control-plane=controller-manager -f
```

### Check CRDs

```bash
kubectl get crd budaiscalers.scaler.bud.studio
```

### Check BudAIScaler Status

```bash
kubectl get budaiscalers -A
kubectl describe budaiscaler <name> -n <namespace>
```
