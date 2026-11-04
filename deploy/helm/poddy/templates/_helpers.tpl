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

{{- define "poddy.bastion.selectorLabels" -}}
app.kubernetes.io/name: {{ printf "%s-bastion" (include "poddy.name" .) | trunc 63 | trimSuffix "-" }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "poddy.bastion.labels" -}}
helm.sh/chart: {{ include "poddy.chart" . }}
{{ include "poddy.bastion.selectorLabels" . }}
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

{{- define "poddy.bastion.serviceAccountName" -}}
{{- if .Values.bastion.serviceAccount.create -}}
{{- default (printf "%s-bastion" (include "poddy.fullname" .)) .Values.bastion.serviceAccount.name -}}
{{- else -}}
{{- required "bastion.serviceAccount.name is required when bastion.serviceAccount.create=false" .Values.bastion.serviceAccount.name -}}
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
