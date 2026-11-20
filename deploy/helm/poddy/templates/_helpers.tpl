{{- define "poddy.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "poddy.fullname" -}}
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

{{- define "poddy.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "poddy.selectorLabels" -}}
app.kubernetes.io/name: {{ printf "%s-portal" (include "poddy.name" .) | trunc 63 | trimSuffix "-" }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "poddy.labels" -}}
helm.sh/chart: {{ include "poddy.chart" . }}
{{ include "poddy.selectorLabels" . }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{- define "poddy.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{- default (include "poddy.fullname" .) .Values.serviceAccount.name -}}
{{- else -}}
{{- required "serviceAccount.name is required when serviceAccount.create=false" .Values.serviceAccount.name -}}
{{- end -}}
{{- end -}}

{{- define "poddy.workspaceNamespace" -}}
{{- .Values.workspaceNamespace | default .Release.Namespace -}}
{{- end -}}

{{- define "poddy.tokenSecretName" -}}
{{- if .Values.auth.existingSecret -}}
{{- .Values.auth.existingSecret -}}
{{- else -}}
{{- printf "%s-token" (include "poddy.fullname" .) -}}
{{- end -}}
{{- end -}}

{{- define "poddy.sshKeysName" -}}
{{- if .Values.ssh.existingKeysSecret -}}
{{- .Values.ssh.existingKeysSecret -}}
{{- else -}}
{{- printf "%s-ssh-keys" (include "poddy.fullname" .) -}}
{{- end -}}
{{- end -}}

{{/*
RBAC ConfigMap name used by the portal.
*/}}
{{- define "poddy.rbacConfigMapName" -}}
{{- if .Values.rbac.configMapName -}}
{{- .Values.rbac.configMapName -}}
{{- else -}}
{{- printf "%s-rbac" (include "poddy.fullname" .) -}}
{{- end -}}
{{- end -}}

{{/*
Workspace state ConfigMap name.
*/}}
{{- define "poddy.stateConfigMapName" -}}
{{- if .Values.workspaceState.configMapName -}}
{{- .Values.workspaceState.configMapName -}}
{{- else -}}
{{- printf "%s-state" (include "poddy.fullname" .) -}}
{{- end -}}
{{- end -}}

{{/*
Secret holding external auth configuration (encryption key and inline provider
client secrets). The portal stores per-user tokens in a different Secret.
*/}}
{{- define "poddy.externalAuthConfigSecretName" -}}
{{- printf "%s-external-auth-config" (include "poddy.fullname" .) -}}
{{- end -}}

{{/*
Secret where the portal stores encrypted per-user external auth tokens.
*/}}
{{- define "poddy.externalAuthTokenSecretName" -}}
{{- if .Values.externalAuth.tokenSecretName -}}
{{- .Values.externalAuth.tokenSecretName -}}
{{- else -}}
{{- printf "%s-external-auth" (include "poddy.fullname" .) -}}
{{- end -}}
{{- end -}}

{{/*
Secret holding the optional PostgreSQL connection string.
*/}}
{{- define "poddy.databaseSecretName" -}}
{{- if .Values.database.existingSecret -}}
{{- .Values.database.existingSecret -}}
{{- else -}}
{{- printf "%s-database" (include "poddy.fullname" .) -}}
{{- end -}}
{{- end -}}

{{- define "poddy.regionsSecretName" -}}
{{- if .Values.regionsExistingSecret -}}
{{- .Values.regionsExistingSecret -}}
{{- else -}}
{{- printf "%s-regions" (include "poddy.fullname" .) -}}
{{- end -}}
{{- end -}}

{{- define "poddy.regionKeySecretName" -}}
{{- if .Values.regionManager.existingSecret -}}
{{- .Values.regionManager.existingSecret -}}
{{- else -}}
{{- printf "%s-region-key" (include "poddy.fullname" .) -}}
{{- end -}}
{{- end -}}

{{- define "poddy.desktopSecretName" -}}
{{- if .Values.desktop.existingSecret -}}
{{- .Values.desktop.existingSecret -}}
{{- else -}}
{{- printf "%s-desktop" (include "poddy.fullname" .) -}}
{{- end -}}
{{- end -}}

{{/*
Secret holding S3 credentials for workspace volume backups.
*/}}
{{- define "poddy.backupSecretName" -}}
{{- if .Values.backup.s3.existingSecret -}}
{{- .Values.backup.s3.existingSecret -}}
{{- else -}}
{{- printf "%s-backup" (include "poddy.fullname" .) -}}
{{- end -}}
{{- end -}}

{{/*
Secret holding the workspace agent HMAC key.
*/}}
{{- define "poddy.agentSecretName" -}}
{{- if .Values.ssh.agent.existingSecret -}}
{{- .Values.ssh.agent.existingSecret -}}
{{- else -}}
{{- printf "%s-agent" (include "poddy.fullname" .) -}}
{{- end -}}
{{- end -}}

{{/*
URL injected into workspace pods for the outbound agent tunnel.
*/}}
{{- define "poddy.agentURL" -}}
{{- if .Values.ssh.agent.url -}}
{{- .Values.ssh.agent.url -}}
{{- else -}}
{{- printf "http://%s-agent.%s.svc:%d" (include "poddy.fullname" .) .Release.Namespace (int .Values.ssh.agent.port) -}}
{{- end -}}
{{- end -}}

{{/*
Image for the poddy-agent sidecar. Defaults to the portal image.
*/}}
{{- define "poddy.agentImage" -}}
{{- if .Values.ssh.agent.image -}}
{{- .Values.ssh.agent.image -}}
{{- else -}}
{{- printf "%s:%s" .Values.image.repository (.Values.image.tag | default .Chart.AppVersion) -}}
{{- end -}}
{{- end -}}

{{/*
Secret holding the SSH certificate authority private key.
*/}}
{{- define "poddy.sshCASecretName" -}}
{{- if .Values.ssh.ca.existingSecret -}}
{{- .Values.ssh.ca.existingSecret -}}
{{- else -}}
{{- printf "%s-ssh-ca" (include "poddy.fullname" .) -}}
{{- end -}}
{{- end -}}

{{- define "poddy.sshHostKeySecretName" -}}
{{- if .Values.ssh.existingHostKeySecret -}}
{{- .Values.ssh.existingHostKeySecret -}}
{{- else -}}
{{- printf "%s-ssh-host-key" (include "poddy.fullname" .) -}}
{{- end -}}
{{- end -}}

{{/*
Headscale public control URL.
*/}}
{{- define "poddy.headscaleURL" -}}
{{- if .Values.mesh.headscale.serverUrl -}}
{{- .Values.mesh.headscale.serverUrl -}}
{{- else -}}
{{- printf "https://%s" .Values.mesh.headscale.host -}}
{{- end -}}
{{- end -}}

{{/*
Mesh control server used by the portal and workspace agents.
*/}}
{{- define "poddy.meshControlURL" -}}
{{- if .Values.mesh.controlUrl -}}
{{- .Values.mesh.controlUrl -}}
{{- else -}}
{{- include "poddy.headscaleURL" . -}}
{{- end -}}
{{- end -}}

{{/*
Secret holding the mesh preauth key.
*/}}
{{- define "poddy.meshSecretName" -}}
{{- if .Values.mesh.existingSecret -}}
{{- .Values.mesh.existingSecret -}}
{{- else -}}
{{- printf "%s-mesh" (include "poddy.fullname" .) -}}
{{- end -}}
{{- end -}}

{{/*
Dex issuer. Dex is served under /dex on the Headscale ingress so deployments
do not need another public hostname.
*/}}
{{- define "poddy.dexIssuer" -}}
{{- printf "%s/dex" (include "poddy.headscaleURL" .) -}}
{{- end -}}

{{/*
OIDC issuer used by Headscale, either the in-chart Dex or an external IdP.
*/}}
{{- define "poddy.oidcIssuer" -}}
{{- if .Values.mesh.dex.enabled -}}
{{- include "poddy.dexIssuer" . -}}
{{- else -}}
{{- .Values.mesh.headscale.oidc.issuer -}}
{{- end -}}
{{- end -}}

{{/*
Secret holding the Headscale OIDC client secret. The same secret is mounted
into Dex when the in-chart identity provider is enabled.
*/}}
{{- define "poddy.headscaleOIDCSecretName" -}}
{{- if .Values.mesh.headscale.oidc.existingSecret -}}
{{- .Values.mesh.headscale.oidc.existingSecret -}}
{{- else -}}
{{- printf "%s-headscale-oidc" (include "poddy.fullname" .) -}}
{{- end -}}
{{- end -}}
