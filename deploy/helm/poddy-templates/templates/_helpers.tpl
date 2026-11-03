{{- define "poddy-templates.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "poddy-templates.fullname" -}}
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

{{- define "poddy-templates.configmap" -}}
{{- $root := .root -}}
{{- $version := .version | toString -}}
{{- $versionName := regexReplaceAll "[^a-zA-Z0-9.-]" $version "-" | trunc 63 | trimSuffix "-" -}}
{{- $baseName := include "poddy-templates.fullname" $root -}}
{{- if $version -}}
{{- $baseName = printf "%s-%s-%s" $baseName .name $versionName -}}
{{- else -}}
{{- $baseName = printf "%s-%s" $baseName .name -}}
{{- end -}}
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: {{ $baseName | trunc 63 | trimSuffix "-" }}
  namespace: {{ $root.Release.Namespace }}
  labels:
    app.kubernetes.io/name: poddy-templates
    app.kubernetes.io/instance: {{ $root.Release.Name }}
    app.kubernetes.io/managed-by: {{ $root.Release.Service }}
    poddy.io/template: "true"
    poddy.io/template-name: {{ .name | quote }}
    {{- if $version }}
    poddy.io/template-version: {{ $version | quote }}
    {{- end }}
    {{- if .active }}
    poddy.io/template-active: "true"
    {{- end }}
data:
  template.yaml: |
    {{- .template | nindent 4 }}
  pod.yaml: |
    {{- .pod | nindent 4 }}
{{- end }}
