{{/*
Expand the name of the chart.
*/}}
{{- define "kubeDeploymentCoordinator.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
We truncate at 63 chars because some Kubernetes name fields are limited to this (by the DNS naming spec).
If release name contains chart name it will be used as a full name.
*/}}
{{- define "kubeDeploymentCoordinator.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
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
{{- define "kubeDeploymentCoordinator.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}

{{- define "kubeDeploymentCoordinator.baseLabels" -}}
helm.sh/chart: {{ include "kubeDeploymentCoordinator.chart" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{- define "kubeDeploymentCoordinator.labels" -}}
{{ include "kubeDeploymentCoordinator.baseLabels" . }}
{{ include "kubeDeploymentCoordinator.selectorLabels" . }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "kubeDeploymentCoordinator.selectorLabels" -}}
app.kubernetes.io/name: {{ include "kubeDeploymentCoordinator.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/component: operator
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "kubeDeploymentCoordinator.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "kubeDeploymentCoordinator.name" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}
