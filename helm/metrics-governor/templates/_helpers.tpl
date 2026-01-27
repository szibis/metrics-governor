{{/*
Expand the name of the chart.
*/}}
{{- define "metrics-governor.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "metrics-governor.fullname" -}}
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
{{- define "metrics-governor.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "metrics-governor.labels" -}}
helm.sh/chart: {{ include "metrics-governor.chart" . }}
{{ include "metrics-governor.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- with .Values.extraLabels }}
{{ toYaml . }}
{{- end }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "metrics-governor.selectorLabels" -}}
app.kubernetes.io/name: {{ include "metrics-governor.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "metrics-governor.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "metrics-governor.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Create the image name
*/}}
{{- define "metrics-governor.image" -}}
{{- $registry := .Values.global.imageRegistry | default .Values.image.registry -}}
{{- $repository := .Values.image.repository -}}
{{- $tag := .Values.image.tag | default .Chart.AppVersion -}}
{{- if $registry }}
{{- printf "%s/%s:%s" $registry $repository $tag }}
{{- else }}
{{- printf "%s:%s" $repository $tag }}
{{- end }}
{{- end }}

{{/*
Create image pull secrets
*/}}
{{- define "metrics-governor.imagePullSecrets" -}}
{{- $secrets := concat (.Values.global.imagePullSecrets | default list) (.Values.imagePullSecrets | default list) -}}
{{- if $secrets }}
imagePullSecrets:
{{- range $secrets }}
  - name: {{ . }}
{{- end }}
{{- end }}
{{- end }}

{{/*
ConfigMap name for limits configuration
*/}}
{{- define "metrics-governor.limitsConfigMapName" -}}
{{- if .Values.limits.existingConfigMap }}
{{- .Values.limits.existingConfigMap }}
{{- else }}
{{- printf "%s-limits" (include "metrics-governor.fullname" .) }}
{{- end }}
{{- end }}

{{/*
Headless service name
*/}}
{{- define "metrics-governor.headlessServiceName" -}}
{{- printf "%s-headless" (include "metrics-governor.fullname" .) }}
{{- end }}

{{/*
Pod FQDN for StatefulSet
*/}}
{{- define "metrics-governor.podFQDN" -}}
{{- $headlessService := include "metrics-governor.headlessServiceName" . -}}
{{- $clusterDomain := .Values.global.clusterDomainSuffix | default "cluster.local" -}}
{{- printf "%s.%s.svc.%s" $headlessService .Release.Namespace $clusterDomain }}
{{- end }}

{{/*
Generate container arguments
*/}}
{{- define "metrics-governor.args" -}}
{{- $args := list -}}
{{- $args = append $args (printf "-grpc-listen=%s" .Values.config.grpcListen) -}}
{{- $args = append $args (printf "-http-listen=%s" .Values.config.httpListen) -}}
{{- $args = append $args (printf "-stats-addr=%s" .Values.config.statsAddr) -}}
{{- $args = append $args (printf "-exporter-endpoint=%s" .Values.config.exporterEndpoint) -}}
{{- $args = append $args (printf "-exporter-insecure=%t" .Values.config.exporterInsecure) -}}
{{- $args = append $args (printf "-exporter-timeout=%s" .Values.config.exporterTimeout) -}}
{{- $args = append $args (printf "-buffer-size=%d" (int .Values.config.bufferSize)) -}}
{{- $args = append $args (printf "-flush-interval=%s" .Values.config.flushInterval) -}}
{{- $args = append $args (printf "-batch-size=%d" (int .Values.config.batchSize)) -}}
{{- if .Values.config.statsLabels }}
{{- $args = append $args (printf "-stats-labels=%s" .Values.config.statsLabels) -}}
{{- end }}
{{- if .Values.limits.enabled }}
{{- $args = append $args "-limits-config=/etc/metrics-governor/limits.yaml" -}}
{{- $args = append $args (printf "-limits-dry-run=%t" .Values.config.limitsDryRun) -}}
{{- end }}
{{- range .Values.config.extraArgs }}
{{- $args = append $args . -}}
{{- end }}
{{- toYaml $args }}
{{- end }}

{{/*
Container ports
*/}}
{{- define "metrics-governor.containerPorts" -}}
- name: grpc
  containerPort: {{ .Values.ports.grpc }}
  protocol: TCP
- name: http
  containerPort: {{ .Values.ports.http }}
  protocol: TCP
- name: stats
  containerPort: {{ .Values.ports.stats }}
  protocol: TCP
{{- end }}

{{/*
Volume mounts
*/}}
{{- define "metrics-governor.volumeMounts" -}}
{{- if .Values.limits.enabled }}
- name: limits-config
  mountPath: /etc/metrics-governor
  readOnly: true
{{- end }}
{{- if and (eq .Values.kind "statefulset") .Values.persistence.enabled }}
- name: data
  mountPath: /data
{{- end }}
{{- with .Values.extraVolumeMounts }}
{{ toYaml . }}
{{- end }}
{{- end }}

{{/*
Volumes
*/}}
{{- define "metrics-governor.volumes" -}}
{{- if .Values.limits.enabled }}
- name: limits-config
  configMap:
    name: {{ include "metrics-governor.limitsConfigMapName" . }}
{{- end }}
{{- with .Values.extraVolumes }}
{{ toYaml . }}
{{- end }}
{{- end }}

{{/*
Pod spec template
*/}}
{{- define "metrics-governor.podSpec" -}}
{{- with .Values.imagePullSecrets }}
imagePullSecrets:
  {{- toYaml . | nindent 2 }}
{{- end }}
serviceAccountName: {{ include "metrics-governor.serviceAccountName" . }}
{{- with .Values.podSecurityContext }}
securityContext:
  {{- toYaml . | nindent 2 }}
{{- end }}
{{- if .Values.priorityClassName }}
priorityClassName: {{ .Values.priorityClassName }}
{{- end }}
{{- if .Values.hostNetwork }}
hostNetwork: true
{{- end }}
{{- if .Values.dnsPolicy }}
dnsPolicy: {{ .Values.dnsPolicy }}
{{- end }}
{{- with .Values.dnsConfig }}
dnsConfig:
  {{- toYaml . | nindent 2 }}
{{- end }}
terminationGracePeriodSeconds: {{ .Values.terminationGracePeriodSeconds }}
{{- with .Values.initContainers }}
initContainers:
  {{- toYaml . | nindent 2 }}
{{- end }}
containers:
  - name: {{ .Chart.Name }}
    {{- with .Values.securityContext }}
    securityContext:
      {{- toYaml . | nindent 6 }}
    {{- end }}
    image: {{ include "metrics-governor.image" . }}
    imagePullPolicy: {{ .Values.image.pullPolicy }}
    args:
      {{- include "metrics-governor.args" . | nindent 6 }}
    ports:
      {{- include "metrics-governor.containerPorts" . | nindent 6 }}
    {{- with .Values.env }}
    env:
      {{- toYaml . | nindent 6 }}
    {{- end }}
    {{- with .Values.envFrom }}
    envFrom:
      {{- toYaml . | nindent 6 }}
    {{- end }}
    {{- if .Values.livenessProbe.enabled }}
    livenessProbe:
      {{- $probe := omit .Values.livenessProbe "enabled" }}
      {{- toYaml $probe | nindent 6 }}
    {{- end }}
    {{- if .Values.readinessProbe.enabled }}
    readinessProbe:
      {{- $probe := omit .Values.readinessProbe "enabled" }}
      {{- toYaml $probe | nindent 6 }}
    {{- end }}
    {{- if .Values.startupProbe.enabled }}
    startupProbe:
      {{- $probe := omit .Values.startupProbe "enabled" }}
      {{- toYaml $probe | nindent 6 }}
    {{- end }}
    {{- with .Values.resources }}
    resources:
      {{- toYaml . | nindent 6 }}
    {{- end }}
    {{- $mounts := include "metrics-governor.volumeMounts" . }}
    {{- if $mounts }}
    volumeMounts:
      {{- $mounts | nindent 6 }}
    {{- end }}
    {{- with .Values.lifecycle }}
    lifecycle:
      {{- toYaml . | nindent 6 }}
    {{- end }}
  {{- with .Values.extraContainers }}
  {{- toYaml . | nindent 2 }}
  {{- end }}
{{- $vols := include "metrics-governor.volumes" . }}
{{- if $vols }}
volumes:
  {{- $vols | nindent 2 }}
{{- end }}
{{- with .Values.nodeSelector }}
nodeSelector:
  {{- toYaml . | nindent 2 }}
{{- end }}
{{- with .Values.affinity }}
affinity:
  {{- toYaml . | nindent 2 }}
{{- end }}
{{- with .Values.tolerations }}
tolerations:
  {{- toYaml . | nindent 2 }}
{{- end }}
{{- with .Values.topologySpreadConstraints }}
topologySpreadConstraints:
  {{- toYaml . | nindent 2 }}
{{- end }}
{{- end }}
