# eino-ext Helm chart

Deploys the eino-ext MCP servers and the standalone approval service.

## Parameters

### Global parameters

| Name | Description | Value |
|:----|:-----------|:-----|
| `global.imageRegistry` | Global image registry override | `` |
| `global.imagePullSecrets` | Global image pull secrets | `[]` |

### Approval service parameters

| Name | Description | Value |
|:----|:-----------|:-----|
| `approval.enabled` | Deploy the standalone approval service | `true` |
| `approval.image.registry` | Approval image registry | `ghcr.io` |
| `approval.image.repository` | Approval image repository | `webcenter-fr/eino-ext/approval` |
| `approval.image.tag` | Approval image tag (defaults to Chart.AppVersion) | `` |
| `approval.image.pullPolicy` | Approval image pull policy | `IfNotPresent` |
| `approval.replicaCount` | Approval replica count | `1` |
| `approval.baseURL` | Public approval base URL (defaults to the ingress host) | `` |
| `approval.store.type` | Store backend: memory, file, redis, postgresql or opensearch | `file` |
| `approval.store.path` | File store directory | `/data` |
| `approval.store.redis.addr` | Redis address | `` |
| `approval.store.postgresql.dsn` | PostgreSQL DSN (stored in a Secret) | `` |
| `approval.store.postgresql.existingSecret` | Existing Secret containing the PostgreSQL DSN | `` |
| `approval.store.postgresql.dsnSecretKey` | Key in the PostgreSQL DSN Secret | `postgres-dsn` |
| `approval.store.opensearch.url` | OpenSearch URL | `` |
| `approval.persistence.enabled` | Enable the file-store PVC | `true` |
| `approval.persistence.size` | PVC size | `1Gi` |
| `approval.persistence.storageClass` | PVC storage class | `` |
| `approval.persistence.accessMode` | PVC access mode | `ReadWriteOnce` |
| `approval.receipt.algorithm` | Receipt algorithm: HS256, RS256 or EdDSA | `HS256` |
| `approval.receipt.existingSecret` | Existing Secret containing the receipt secret | `` |
| `approval.receipt.secretKey` | Key in the receipt Secret | `receipt-secret` |
| `approval.oidc.enabled` | Enable OIDC browser login | `false` |
| `approval.oidc.issuer` | OIDC issuer URL | `` |
| `approval.oidc.clientID` | OIDC client ID | `` |
| `approval.oidc.clientSecret` | OIDC client secret (stored in a Secret) | `` |
| `approval.oidc.existingSecret` | Existing Secret containing the OIDC client secret | `` |
| `approval.oidc.clientSecretKey` | Key in the OIDC client secret Secret | `oidc-client-secret` |
| `approval.oidc.redirectURL` | OIDC redirect URL | `` |
| `approval.credentials.existingSecret` | Existing Secret containing the server credential file | `` |
| `approval.credentials.secretKey` | Key in the server credential Secret | `credentials` |
| `approval.policy.rules` | Approver policy rules | `[]` |
| `approval.ingress.enabled` | Enable the approval Ingress | `false` |
| `approval.ingress.className` | Ingress class name | `` |
| `approval.ingress.host` | Ingress host | `` |
| `approval.ingress.tls.enabled` | Enable Ingress TLS | `false` |
| `approval.ingress.tls.secretName` | Ingress TLS secret name | `` |
| `approval.ingress.annotations` | Ingress annotations | `{}` |
| `approval.resources` | Approval resource requests and limits | `{}` |
| `approval.podDisruptionBudget.enabled` | Enable the approval PDB | `false` |
| `approval.podDisruptionBudget.minAvailable` | PDB minAvailable | `1` |
| `approval.autoscaling.enabled` | Enable the approval HPA (requires a shared store) | `false` |
| `approval.autoscaling.minReplicas` | HPA minimum replicas | `1` |
| `approval.autoscaling.maxReplicas` | HPA maximum replicas | `3` |
| `approval.autoscaling.targetCPUUtilizationPercentage` | HPA CPU target | `80` |
| `approval.networkPolicy.enabled` | Enable the approval NetworkPolicy | `false` |
| `approval.serviceAccount.create` | Create the approval ServiceAccount | `true` |
| `approval.serviceAccount.name` | Approval ServiceAccount name override | `` |
| `approval.serviceAccount.annotations` | Approval ServiceAccount annotations | `{}` |
| `approval.podAnnotations` | Approval pod annotations | `{}` |
| `approval.nodeSelector` | Approval node selector | `{}` |
| `approval.tolerations` | Approval tolerations | `[]` |
| `approval.affinity` | Approval affinity | `{}` |
| `approval.securityContext` | Approval container security context | `{}` |
| `approval.podSecurityContext` | Approval pod security context | `{}` |

### MCP server parameters

| Name | Description | Value |
|:----|:-----------|:-----|
| `mcp.image.registry` | MCP image registry | `ghcr.io` |
| `mcp.image.repository` | MCP image repository | `webcenter-fr/eino-ext/mcp` |
| `mcp.image.tag` | MCP image tag (defaults to Chart.AppVersion) | `` |
| `mcp.image.pullPolicy` | MCP image pull policy | `IfNotPresent` |
| `mcp.auth.enabled` | Enable bearer-token authentication | `true` |
| `mcp.auth.existingSecret` | Existing Secret containing the auth token files | `` |
| `mcp.auth.tokens` | Static auth tokens | `[]` |
| `mcp.auth.requiredScopes` | Required token scopes | `[]` |
| `mcp.authorization.enabled` | Enable RBAC | `true` |
| `mcp.authorization.rules[].groups` | Groups allowed by the rule | `[]` |
| `mcp.authorization.rules[].operations` | Operations allowed by the rule | `[read]` |
| `mcp.approval.enabled` | Enable out-of-band approval for write tools | `true` |
| `mcp.networkPolicy.enabled` | Enable the MCP NetworkPolicy | `false` |
| `mcp.networkPolicy.mcpClientSelector` | Ingress client selector | `{}` |
| `mcp.networkPolicy.extraEgress` | Extra egress rules | `[]` |
| `mcp.serviceAccount.create` | Create one ServiceAccount per family | `true` |
| `mcp.serviceAccount.name` | ServiceAccount name override | `` |
| `mcp.serviceAccount.annotations` | ServiceAccount annotations | `{}` |
| `mcp.podAnnotations` | MCP pod annotations | `{}` |
| `mcp.nodeSelector` | MCP node selector | `{}` |
| `mcp.tolerations` | MCP tolerations | `[]` |
| `mcp.affinity` | MCP affinity | `{}` |
| `mcp.resources` | MCP resource requests and limits | `{}` |
| `mcp.securityContext` | MCP container security context | `{}` |
| `mcp.podSecurityContext` | MCP pod security context | `{}` |

### MCP families

| Name | Description | Value |
|:----|:-----------|:-----|
| `mcpServers.kubernetes.enabled` | Enable the Kubernetes MCP server | `true` |
| `mcpServers.kubernetes.rbac.create` | Create the Kubernetes RBAC | `true` |
| `mcpServers.kubernetes.rbac.scope` | RBAC scope: cluster or namespace | `cluster` |
| `mcpServers.kubernetes.rbac.namespaces` | Namespaces for namespace-scoped RBAC | `[]` |
| `mcpServers.kubernetes.rbac.allowWrite` | DANGEROUS: allow resource create/patch/delete/apply | `false` |
| `mcpServers.kubernetes.rbac.allowExec` | DANGEROUS: allow pods/exec (arbitrary command execution) | `false` |
| `mcpServers.kubernetes.clusters.in-cluster.inCluster` | Use the in-cluster service account | `true` |
| `mcpServers.kubernetes.clusters.in-cluster.disallowedNamespaces` | Namespaces the tools refuse to touch | `[kube-system kube-public kube-node-lease]` |
| `mcpServers.kubernetes.clusters.in-cluster.defaultTimeout` | Default operation timeout | `30s` |
| `mcpServers.kubernetes.existingSecret` | Existing Secret containing the cluster kubeconfigs | `` |
| `mcpServers.kubernetes.approvalTokenHash` | Approval token hash when existingSecret is used | `` |
| `mcpServers.kubernetes.replicaCount` | Kubernetes MCP replica count | `1` |
| `mcpServers.kubernetes.service.type` | Kubernetes MCP Service type | `ClusterIP` |
| `mcpServers.kubernetes.service.port` | Kubernetes MCP Service port | `8080` |
| `mcpServers.kubernetes.ingress.enabled` | Enable the Kubernetes MCP Ingress | `false` |
| `mcpServers.kubernetes.ingress.className` | Ingress class name | `` |
| `mcpServers.kubernetes.ingress.host` | Ingress host | `` |
| `mcpServers.kubernetes.ingress.tls.enabled` | Enable Ingress TLS | `false` |
| `mcpServers.kubernetes.ingress.tls.secretName` | Ingress TLS secret name | `` |
| `mcpServers.kubernetes.ingress.annotations` | Ingress annotations | `{}` |
| `mcpServers.kubernetes.podDisruptionBudget.enabled` | Enable the Kubernetes MCP PDB | `false` |
| `mcpServers.kubernetes.podDisruptionBudget.minAvailable` | PDB minAvailable | `1` |
| `mcpServers.kubernetes.autoscaling.enabled` | Enable the Kubernetes MCP HPA | `false` |
| `mcpServers.kubernetes.autoscaling.minReplicas` | HPA minimum replicas | `1` |
| `mcpServers.kubernetes.autoscaling.maxReplicas` | HPA maximum replicas | `3` |
| `mcpServers.kubernetes.autoscaling.targetCPUUtilizationPercentage` | HPA CPU target | `80` |
| `mcpServers.kubernetes.resources` | Kubernetes MCP resource requests and limits | `{}` |
| `mcpServers.kubernetes.podAnnotations` | Kubernetes MCP pod annotations | `{}` |
| `mcpServers.kubernetes.nodeSelector` | Kubernetes MCP node selector | `{}` |
| `mcpServers.kubernetes.tolerations` | Kubernetes MCP tolerations | `[]` |
| `mcpServers.kubernetes.affinity` | Kubernetes MCP affinity | `{}` |
| `mcpServers.argocd.enabled` | Enable the ArgoCD MCP server | `false` |
| `mcpServers.argocd.instances` | ArgoCD instances | `{}` |
| `mcpServers.argocd.existingSecret` | Existing Secret containing the ArgoCD tokens | `` |
| `mcpServers.argocd.approvalTokenHash` | Approval token hash when existingSecret is used | `` |
| `mcpServers.argocd.replicaCount` | ArgoCD MCP replica count | `1` |
| `mcpServers.argocd.service.type` | ArgoCD MCP Service type | `ClusterIP` |
| `mcpServers.argocd.service.port` | ArgoCD MCP Service port | `8080` |
| `mcpServers.argocd.ingress.enabled` | Enable the ArgoCD MCP Ingress | `false` |
| `mcpServers.argocd.ingress.className` | Ingress class name | `` |
| `mcpServers.argocd.ingress.host` | Ingress host | `` |
| `mcpServers.argocd.ingress.tls.enabled` | Enable Ingress TLS | `false` |
| `mcpServers.argocd.ingress.tls.secretName` | Ingress TLS secret name | `` |
| `mcpServers.argocd.ingress.annotations` | Ingress annotations | `{}` |
| `mcpServers.argocd.podDisruptionBudget.enabled` | Enable the ArgoCD MCP PDB | `false` |
| `mcpServers.argocd.podDisruptionBudget.minAvailable` | PDB minAvailable | `1` |
| `mcpServers.argocd.autoscaling.enabled` | Enable the ArgoCD MCP HPA | `false` |
| `mcpServers.argocd.autoscaling.minReplicas` | HPA minimum replicas | `1` |
| `mcpServers.argocd.autoscaling.maxReplicas` | HPA maximum replicas | `3` |
| `mcpServers.argocd.autoscaling.targetCPUUtilizationPercentage` | HPA CPU target | `80` |
| `mcpServers.argocd.resources` | ArgoCD MCP resource requests and limits | `{}` |
| `mcpServers.argocd.podAnnotations` | ArgoCD MCP pod annotations | `{}` |
| `mcpServers.argocd.nodeSelector` | ArgoCD MCP node selector | `{}` |
| `mcpServers.argocd.tolerations` | ArgoCD MCP tolerations | `[]` |
| `mcpServers.argocd.affinity` | ArgoCD MCP affinity | `{}` |
| `mcpServers.prometheus.enabled` | Enable the Prometheus MCP server | `false` |
| `mcpServers.prometheus.instances` | Prometheus instances | `{}` |
| `mcpServers.prometheus.existingSecret` | Existing Secret containing the Prometheus tokens | `` |
| `mcpServers.prometheus.approvalTokenHash` | Approval token hash when existingSecret is used | `` |
| `mcpServers.prometheus.replicaCount` | Prometheus MCP replica count | `1` |
| `mcpServers.prometheus.service.type` | Prometheus MCP Service type | `ClusterIP` |
| `mcpServers.prometheus.service.port` | Prometheus MCP Service port | `8080` |
| `mcpServers.prometheus.ingress.enabled` | Enable the Prometheus MCP Ingress | `false` |
| `mcpServers.prometheus.ingress.className` | Ingress class name | `` |
| `mcpServers.prometheus.ingress.host` | Ingress host | `` |
| `mcpServers.prometheus.ingress.tls.enabled` | Enable Ingress TLS | `false` |
| `mcpServers.prometheus.ingress.tls.secretName` | Ingress TLS secret name | `` |
| `mcpServers.prometheus.ingress.annotations` | Ingress annotations | `{}` |
| `mcpServers.prometheus.podDisruptionBudget.enabled` | Enable the Prometheus MCP PDB | `false` |
| `mcpServers.prometheus.podDisruptionBudget.minAvailable` | PDB minAvailable | `1` |
| `mcpServers.prometheus.autoscaling.enabled` | Enable the Prometheus MCP HPA | `false` |
| `mcpServers.prometheus.autoscaling.minReplicas` | HPA minimum replicas | `1` |
| `mcpServers.prometheus.autoscaling.maxReplicas` | HPA maximum replicas | `3` |
| `mcpServers.prometheus.autoscaling.targetCPUUtilizationPercentage` | HPA CPU target | `80` |
| `mcpServers.prometheus.resources` | Prometheus MCP resource requests and limits | `{}` |
| `mcpServers.prometheus.podAnnotations` | Prometheus MCP pod annotations | `{}` |
| `mcpServers.prometheus.nodeSelector` | Prometheus MCP node selector | `{}` |
| `mcpServers.prometheus.tolerations` | Prometheus MCP tolerations | `[]` |
| `mcpServers.prometheus.affinity` | Prometheus MCP affinity | `{}` |
| `mcpServers.grafana.enabled` | Enable the Grafana MCP server | `false` |
| `mcpServers.grafana.instances` | Grafana instances | `{}` |
| `mcpServers.grafana.existingSecret` | Existing Secret containing the Grafana tokens | `` |
| `mcpServers.grafana.approvalTokenHash` | Approval token hash when existingSecret is used | `` |
| `mcpServers.grafana.replicaCount` | Grafana MCP replica count | `1` |
| `mcpServers.grafana.service.type` | Grafana MCP Service type | `ClusterIP` |
| `mcpServers.grafana.service.port` | Grafana MCP Service port | `8080` |
| `mcpServers.grafana.ingress.enabled` | Enable the Grafana MCP Ingress | `false` |
| `mcpServers.grafana.ingress.className` | Ingress class name | `` |
| `mcpServers.grafana.ingress.host` | Ingress host | `` |
| `mcpServers.grafana.ingress.tls.enabled` | Enable Ingress TLS | `false` |
| `mcpServers.grafana.ingress.tls.secretName` | Ingress TLS secret name | `` |
| `mcpServers.grafana.ingress.annotations` | Ingress annotations | `{}` |
| `mcpServers.grafana.podDisruptionBudget.enabled` | Enable the Grafana MCP PDB | `false` |
| `mcpServers.grafana.podDisruptionBudget.minAvailable` | PDB minAvailable | `1` |
| `mcpServers.grafana.autoscaling.enabled` | Enable the Grafana MCP HPA | `false` |
| `mcpServers.grafana.autoscaling.minReplicas` | HPA minimum replicas | `1` |
| `mcpServers.grafana.autoscaling.maxReplicas` | HPA maximum replicas | `3` |
| `mcpServers.grafana.autoscaling.targetCPUUtilizationPercentage` | HPA CPU target | `80` |
| `mcpServers.grafana.resources` | Grafana MCP resource requests and limits | `{}` |
| `mcpServers.grafana.podAnnotations` | Grafana MCP pod annotations | `{}` |
| `mcpServers.grafana.nodeSelector` | Grafana MCP node selector | `{}` |
| `mcpServers.grafana.tolerations` | Grafana MCP tolerations | `[]` |
| `mcpServers.grafana.affinity` | Grafana MCP affinity | `{}` |
