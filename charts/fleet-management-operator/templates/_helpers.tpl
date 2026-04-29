{{/*
Expand the name of the chart.
*/}}
{{- define "fleet-management-operator.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "fleet-management-operator.fullname" -}}
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
{{- define "fleet-management-operator.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "fleet-management-operator.labels" -}}
helm.sh/chart: {{ include "fleet-management-operator.chart" . }}
{{ include "fleet-management-operator.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "fleet-management-operator.selectorLabels" -}}
app.kubernetes.io/name: {{ include "fleet-management-operator.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
control-plane: controller-manager
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "fleet-management-operator.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "fleet-management-operator.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Create the name of the secret to use for Fleet Management credentials
*/}}
{{- define "fleet-management-operator.secretName" -}}
{{- if .Values.fleetManagement.existingSecret }}
{{- .Values.fleetManagement.existingSecret }}
{{- else }}
{{- include "fleet-management-operator.fullname" . }}-credentials
{{- end }}
{{- end }}

{{/*
Whether the chart should render Kubernetes admission webhook resources.
Keep this aligned with the chart-exposed controller/webhook toggles.
*/}}
{{- define "fleet-management-operator.webhooksEnabled" -}}
{{- if or .Values.controllers.pipeline.enabled .Values.controllers.collector.enabled .Values.controllers.collectorDiscovery.enabled .Values.controllers.pipelineDiscovery.enabled .Values.controllers.externalAttributeSync.enabled .Values.controllers.remoteAttributePolicy.enabled .Values.controllers.tenantPolicy.enabled -}}
true
{{- end -}}
{{- end }}
