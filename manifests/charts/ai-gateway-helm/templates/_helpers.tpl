{*
 Copyright Envoy AI Gateway Authors
 SPDX-License-Identifier: Apache-2.0
 The full text of the Apache license is available in the LICENSE file at
 the root of the repo.
*}

{{/*
Expand the name of the chart.
*/}}
{{- define "ai-gateway-helm.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
We truncate at 63 chars because some Kubernetes name fields are limited to this (by the DNS naming spec).
If release name contains chart name it will be used as a full name.
*/}}
{{- define "ai-gateway-helm.controller.fullname" -}}
{{- if .Values.controller.fullnameOverride }}
{{- .Values.controller.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.controller.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{/*
Create chart name and version as used by the chart label.
*/}}
{{- define "ai-gateway-helm.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "ai-gateway-helm.labels" -}}
helm.sh/chart: {{ include "ai-gateway-helm.chart" . }}
{{ include "ai-gateway-helm.controller.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "ai-gateway-helm.controller.selectorLabels" -}}
app.kubernetes.io/name: {{ include "ai-gateway-helm.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "ai-gateway-helm.controller.serviceAccountName" -}}
{{- if .Values.controller.serviceAccount.create }}
{{- default (include "ai-gateway-helm.controller.fullname" .) .Values.controller.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.controller.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Convert extraEnvVars array to semicolon-separated string for extProc
*/}}
{{- define "ai-gateway-helm.extProc.envVarsString" -}}
{{- $envVars := list -}}
{{- range .Values.extProc.extraEnvVars -}}
  {{- $envVars = append $envVars (printf "%s=%s" .name .value) -}}
{{- end -}}
{{- join ";" $envVars -}}
{{- end }}

{{/*
Convert imagePullSecrets array to semicolon-separated string for extProc
*/}}
{{- define "ai-gateway-helm.extProc.imagePullSecretsString" -}}
{{- $secrets := list -}}
{{- range .Values.extProc.imagePullSecrets -}}
  {{- $secrets = append $secrets .name -}}
{{- end -}}
{{- join ";" $secrets -}}
{{- end }}
