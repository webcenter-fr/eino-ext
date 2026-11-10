{{/*
Expand the chart name.
*/}}
{{- define "eino-ext.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Expand the chart full name.
*/}}
{{- define "eino-ext.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- $name := default .Chart.Name .Values.nameOverride -}}
{{- if contains $name .Release.Name -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{/*
Common labels.
*/}}
{{- define "eino-ext.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
app.kubernetes.io/name: {{ include "eino-ext.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{/*
Selector labels.
*/}}
{{- define "eino-ext.selectorLabels" -}}
app.kubernetes.io/name: {{ include "eino-ext.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{/*
Image reference. Expects a dict with root and image.
*/}}
{{- define "eino-ext.image" -}}
{{- $registry := .image.registry -}}
{{- if .root.Values.global.imageRegistry -}}
{{- $registry = .root.Values.global.imageRegistry -}}
{{- end -}}
{{- $tag := default .root.Chart.AppVersion .image.tag -}}
{{- printf "%s/%s:%s" $registry .image.repository $tag -}}
{{- end -}}

{{/*
ServiceAccount name. Expects a dict with root, component and serviceAccount.
*/}}
{{- define "eino-ext.serviceAccountName" -}}
{{- if .serviceAccount.create -}}
{{- default (printf "%s-%s" (include "eino-ext.fullname" .root) .component) .serviceAccount.name -}}
{{- else -}}
{{- default "default" .serviceAccount.name -}}
{{- end -}}
{{- end -}}

{{/*
Approval base URL: explicit value, else the ingress host, else the in-cluster
Service DNS.
*/}}
{{- define "eino-ext.approvalBaseURL" -}}
{{- $approval := .Values.approval -}}
{{- if $approval.baseURL -}}
{{- $approval.baseURL -}}
{{- else if and $approval.ingress.enabled $approval.ingress.host -}}
{{- if $approval.ingress.tls.enabled -}}
{{- printf "https://%s" $approval.ingress.host -}}
{{- else -}}
{{- printf "http://%s" $approval.ingress.host -}}
{{- end -}}
{{- else -}}
{{- printf "http://%s-approval:8080" (include "eino-ext.fullname" .) -}}
{{- end -}}
{{- end -}}

{{/*
Whether a family exposes write tools (and therefore needs approval).
*/}}
{{- define "eino-ext.familyHasWriteTools" -}}
{{- if eq . "prometheus" -}}
{{- else -}}
true
{{- end -}}
{{- end -}}

{{/*
Comma-separated instance names for a family (clusters for kubernetes).
*/}}
{{- define "eino-ext.familyInstances" -}}
{{- $out := list -}}
{{- if eq .name "kubernetes" -}}
{{- range $clusterName, $_ := .cfg.clusters -}}{{- $out = append $out $clusterName -}}{{- end -}}
{{- else -}}
{{- range $instanceName, $_ := .cfg.instances -}}{{- $out = append $out $instanceName -}}{{- end -}}
{{- end -}}
{{- join "," $out -}}
{{- end -}}

{{/*
Name of the generated family Secret.
*/}}
{{- define "eino-ext.familySecretName" -}}
{{- printf "%s-%s" (include "eino-ext.fullname" .root) .name -}}
{{- end -}}

{{/*
Name of the shared receipt Secret.
*/}}
{{- define "eino-ext.receiptSecretName" -}}
{{- default (printf "%s-receipt" (include "eino-ext.fullname" .)) .Values.approval.receipt.existingSecret -}}
{{- end -}}

{{/*
Name of the approval credentials Secret.
*/}}
{{- define "eino-ext.credentialsSecretName" -}}
{{- default (printf "%s-approval-credentials" (include "eino-ext.fullname" .)) .Values.approval.credentials.existingSecret -}}
{{- end -}}

{{/*
Name of the OIDC client secret Secret.
*/}}
{{- define "eino-ext.oidcSecretName" -}}
{{- default (printf "%s-approval-oidc" (include "eino-ext.fullname" .)) .Values.approval.oidc.existingSecret -}}
{{- end -}}

{{/*
Name of the PostgreSQL DSN Secret.
*/}}
{{- define "eino-ext.postgresSecretName" -}}
{{- default (printf "%s-approval-postgresql" (include "eino-ext.fullname" .)) .Values.approval.store.postgresql.existingSecret -}}
{{- end -}}

{{/*
Name of the MCP auth Secret.
*/}}
{{- define "eino-ext.authSecretName" -}}
{{- default (printf "%s-mcp-auth" (include "eino-ext.fullname" .)) .Values.mcp.auth.existingSecret -}}
{{- end -}}

{{/*
Check an existing Secret contains the required keys. lookup returns empty
during helm template, so the check is skipped there.
*/}}
{{- define "eino-ext.checkExistingSecret" -}}
{{- $secret := lookup "v1" "Secret" .namespace .name -}}
{{- if $secret -}}
{{- range $key := .keys -}}
{{- if not (hasKey $secret.data $key) -}}
{{- fail (printf "existing Secret %s is missing key %s" $.name $key) -}}
{{- end -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{/*
Chart guards.
*/}}
{{- define "eino-ext.validate" -}}
{{- $root := . -}}
{{- $approval := .Values.approval -}}
{{- if and $approval.enabled (or (gt (int $approval.replicaCount) 1) $approval.autoscaling.enabled) (has $approval.store.type (list "memory" "file")) -}}
{{- fail "approval.replicaCount > 1 or approval.autoscaling.enabled is not supported with a memory or file store; use redis, postgresql or opensearch" -}}
{{- end -}}
{{- if and $approval.enabled (not .Values.mcp.approval.enabled) -}}
{{- fail "approval.enabled=true requires mcp.approval.enabled=true" -}}
{{- end -}}
{{- if and (not $approval.enabled) .Values.mcp.approval.enabled -}}
{{- fail "mcp.approval.enabled=true requires approval.enabled=true" -}}
{{- end -}}
{{- if and $approval.enabled $approval.oidc.enabled (or (not $approval.oidc.issuer) (not $approval.oidc.clientID)) -}}
{{- fail "approval.oidc.enabled requires approval.oidc.issuer and approval.oidc.clientID" -}}
{{- end -}}
{{- if and $approval.enabled $approval.oidc.enabled (not $approval.oidc.existingSecret) (not $approval.oidc.clientSecret) -}}
{{- fail "approval.oidc.enabled requires approval.oidc.clientSecret or approval.oidc.existingSecret (a generated client secret cannot authenticate to the IdP)" -}}
{{- end -}}
{{- if and $approval.enabled (eq $approval.store.type "postgresql") (not $approval.store.postgresql.dsn) (not $approval.store.postgresql.existingSecret) -}}
{{- fail "approval.store.type=postgresql requires approval.store.postgresql.dsn or approval.store.postgresql.existingSecret" -}}
{{- end -}}
{{- if and $approval.enabled (eq $approval.store.type "file") (not $approval.persistence.enabled) -}}
{{- fail "approval.store.type=file requires approval.persistence.enabled=true (the root filesystem is read-only and no volume is mounted at the store path)" -}}
{{- end -}}
{{- $writeFamilies := list -}}
{{- $dangerous := false -}}
{{- range $name, $cfg := .Values.mcpServers -}}
{{- if $cfg.enabled -}}
{{- if eq $name "kubernetes" -}}
{{- if not $cfg.clusters -}}{{- fail (printf "mcpServers.%s is enabled but has no clusters" $name) -}}{{- end -}}
{{- if or $cfg.rbac.allowWrite $cfg.rbac.allowExec -}}{{- $dangerous = true -}}{{- end -}}
{{- else -}}
{{- if not $cfg.instances -}}{{- fail (printf "mcpServers.%s is enabled but has no instances" $name) -}}{{- end -}}
{{- end -}}
{{- if include "eino-ext.familyHasWriteTools" $name -}}{{- $writeFamilies = append $writeFamilies $name -}}{{- end -}}
{{- end -}}
{{- end -}}
{{- if and (not .Values.mcp.auth.enabled) $dangerous -}}
{{- fail "mcp.auth.enabled=false is not allowed while allowWrite or allowExec is enabled" -}}
{{- end -}}
{{- if and $approval.enabled (eq (len $writeFamilies) 0) -}}
{{- fail "approval.enabled=true requires at least one write-capable family; disable approval or enable kubernetes/argocd/grafana" -}}
{{- end -}}
{{- if $approval.receipt.existingSecret -}}
{{- include "eino-ext.checkExistingSecret" (dict "namespace" $root.Release.Namespace "name" $approval.receipt.existingSecret "keys" (list $approval.receipt.secretKey)) -}}
{{- end -}}
{{- if and $approval.oidc.enabled $approval.oidc.existingSecret -}}
{{- include "eino-ext.checkExistingSecret" (dict "namespace" $root.Release.Namespace "name" $approval.oidc.existingSecret "keys" (list $approval.oidc.clientSecretKey)) -}}
{{- end -}}
{{- if $approval.credentials.existingSecret -}}
{{- include "eino-ext.checkExistingSecret" (dict "namespace" $root.Release.Namespace "name" $approval.credentials.existingSecret "keys" (list $approval.credentials.secretKey)) -}}
{{- end -}}
{{- if and (eq $approval.store.type "postgresql") $approval.store.postgresql.existingSecret -}}
{{- include "eino-ext.checkExistingSecret" (dict "namespace" $root.Release.Namespace "name" $approval.store.postgresql.existingSecret "keys" (list $approval.store.postgresql.dsnSecretKey)) -}}
{{- end -}}
{{- if .Values.mcp.auth.existingSecret -}}
{{- $keys := list -}}
{{- range $token := .Values.mcp.auth.tokens -}}
{{- $keys = append $keys (default (printf "%s-token" $token.name) $token.tokenKey) -}}
{{- end -}}
{{- if not .Values.mcp.auth.tokens -}}{{- $keys = append $keys "admin-token" -}}{{- end -}}
{{- include "eino-ext.checkExistingSecret" (dict "namespace" $root.Release.Namespace "name" .Values.mcp.auth.existingSecret "keys" $keys) -}}
{{- end -}}
{{- range $name, $cfg := .Values.mcpServers -}}
{{- if and $cfg.enabled $cfg.existingSecret -}}
{{- $keys := list -}}
{{- if eq $name "kubernetes" -}}
{{- range $clusterName, $cluster := $cfg.clusters -}}{{- if not $cluster.inCluster -}}{{- $keys = append $keys (default (printf "%s-kubeconfig" $clusterName) $cluster.kubeconfigSecretKey) -}}{{- end -}}{{- end -}}
{{- else -}}
{{- range $instanceName, $instance := $cfg.instances -}}{{- $keys = append $keys (default (printf "%s-token" $instanceName) $instance.tokenKey) -}}{{- end -}}
{{- end -}}
{{- if and $root.Values.mcp.approval.enabled (include "eino-ext.familyHasWriteTools" $name) -}}{{- $keys = append $keys "approval-token" -}}{{- end -}}
{{- include "eino-ext.checkExistingSecret" (dict "namespace" $root.Release.Namespace "name" $cfg.existingSecret "keys" $keys) -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{/*
MCP server YAML configuration. Expects a dict with root, name and cfg.
*/}}
{{- define "eino-ext.mcpConfig" -}}
{{- $root := .root -}}
{{- $name := .name -}}
{{- $cfg := .cfg -}}
server: {{ $name }}
transport: http
addr: ":8080"
opsAddr: ":9090"
log:
  level: info
  format: json
{{- if eq $name "kubernetes" }}
kubernetes:
  clusters:
{{- range $clusterName, $cluster := $cfg.clusters }}
    {{ $clusterName }}:
{{- if $cluster.inCluster }}
      inCluster: true
{{- else }}
      kubeconfigFile: /etc/eino-ext/secrets/{{ $name }}/{{ default (printf "%s-kubeconfig" $clusterName) $cluster.kubeconfigSecretKey }}
{{- if $cluster.context }}
      context: {{ $cluster.context | quote }}
{{- end }}
{{- end }}
{{- if $cluster.disallowedNamespaces }}
      disallowedNamespaces:
{{ toYaml $cluster.disallowedNamespaces | indent 8 }}
{{- end }}
{{- if $cluster.defaultTimeout }}
      defaultTimeout: {{ $cluster.defaultTimeout }}
{{- end }}
{{- end }}
{{- else }}
{{ $name }}:
  instances:
{{- range $instanceName, $instance := $cfg.instances }}
    {{ $instanceName }}:
      url: {{ $instance.url | quote }}
      tokenFile: /etc/eino-ext/secrets/{{ $name }}/{{ default (printf "%s-token" $instanceName) $instance.tokenKey }}
{{- if $instance.tlsSkipVerify }}
      tlsSkipVerify: true
{{- end }}
{{- if $instance.defaultTimeout }}
      defaultTimeout: {{ $instance.defaultTimeout }}
{{- end }}
{{- end }}
{{- end }}
{{- if $root.Values.mcp.auth.enabled }}
auth:
  providers:
    - type: local
      local:
        tokens:
{{- range $token := $root.Values.mcp.auth.tokens }}
          - tokenFile: /etc/eino-ext/secrets/auth/{{ default (printf "%s-token" $token.name) $token.tokenKey }}
            user: {{ $token.user | quote }}
{{- if $token.groups }}
            groups:
{{ toYaml $token.groups | indent 14 }}
{{- end }}
{{- if $token.scopes }}
            scopes:
{{ toYaml $token.scopes | indent 14 }}
{{- end }}
{{- end }}
{{- if not $root.Values.mcp.auth.tokens }}
          - tokenFile: /etc/eino-ext/secrets/auth/admin-token
            user: admin
            scopes: [mcp]
{{- end }}
{{- if $root.Values.mcp.auth.requiredScopes }}
  requiredScopes:
{{ toYaml $root.Values.mcp.auth.requiredScopes | indent 4 }}
{{- end }}
{{- end }}
{{- if $root.Values.mcp.authorization.enabled }}
authz:
  rules:
{{ toYaml $root.Values.mcp.authorization.rules | indent 4 }}
{{- end }}
{{- if and $root.Values.mcp.approval.enabled (include "eino-ext.familyHasWriteTools" $name) }}
approval:
  timeout: 5m
  service:
    url: http://{{ include "eino-ext.fullname" $root }}-approval:8080
    serverName: {{ $name }}
    serverTokenFile: /etc/eino-ext/secrets/{{ $name }}/approval-token
    receiptSecretFile: /etc/eino-ext/secrets/receipt-secret
    receiptAlgorithm: {{ $root.Values.approval.receipt.algorithm }}
{{- end }}
{{- end -}}

{{/*
Approval service YAML configuration.
*/}}
{{- define "eino-ext.approvalConfig" -}}
{{- $root := . -}}
{{- $approval := .Values.approval -}}
addr: ":8080"
baseURL: {{ include "eino-ext.approvalBaseURL" $root | quote }}
store: {{ $approval.store.type }}
{{- if eq $approval.store.type "file" }}
storeDir: {{ $approval.store.path | quote }}
{{- end }}
{{- if eq $approval.store.type "redis" }}
redisAddr: {{ $approval.store.redis.addr | quote }}
{{- end }}
{{- if eq $approval.store.type "postgresql" }}
postgresDSNFile: /run/secrets/postgres-dsn
{{- end }}
{{- if eq $approval.store.type "opensearch" }}
openSearchURL: {{ $approval.store.opensearch.url | quote }}
{{- end }}
receiptAlg: {{ $approval.receipt.algorithm }}
receiptSecretFile: /run/secrets/receipt-secret
{{- if $approval.oidc.enabled }}
oidcIssuer: {{ $approval.oidc.issuer | quote }}
oidcClientID: {{ $approval.oidc.clientID | quote }}
oidcClientSecretFile: /run/secrets/oidc-client-secret
oidcRedirectURL: {{ $approval.oidc.redirectURL | quote }}
{{- end }}
policyFile: /etc/eino-ext/policy.json
serverCredentialFile: /run/secrets/credentials
logLevel: info
logFormat: json
{{- end -}}
