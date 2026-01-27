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
{{- $args = append $args (printf "-exporter-protocol=%s" .Values.config.exporterProtocol) -}}
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
{{/* Receiver TLS */}}
{{- if .Values.receiverTLS.enabled }}
{{- $args = append $args "-receiver-tls-enabled=true" -}}
{{- $args = append $args "-receiver-tls-cert=/etc/tls/receiver/tls.crt" -}}
{{- $args = append $args "-receiver-tls-key=/etc/tls/receiver/tls.key" -}}
{{- if .Values.receiverTLS.caSecretName }}
{{- $args = append $args "-receiver-tls-ca=/etc/tls/receiver-ca/ca.crt" -}}
{{- end }}
{{- if .Values.receiverTLS.clientAuth }}
{{- $args = append $args "-receiver-tls-client-auth=true" -}}
{{- end }}
{{- end }}
{{/* Receiver Auth */}}
{{- if .Values.receiverAuth.enabled }}
{{- $args = append $args "-receiver-auth-enabled=true" -}}
{{- end }}
{{/* Exporter TLS */}}
{{- if .Values.exporterTLS.enabled }}
{{- $args = append $args "-exporter-tls-enabled=true" -}}
{{- if .Values.exporterTLS.secretName }}
{{- $args = append $args "-exporter-tls-cert=/etc/tls/exporter/tls.crt" -}}
{{- $args = append $args "-exporter-tls-key=/etc/tls/exporter/tls.key" -}}
{{- end }}
{{- if .Values.exporterTLS.caSecretName }}
{{- $args = append $args "-exporter-tls-ca=/etc/tls/exporter-ca/ca.crt" -}}
{{- end }}
{{- if .Values.exporterTLS.insecureSkipVerify }}
{{- $args = append $args "-exporter-tls-skip-verify=true" -}}
{{- end }}
{{- if .Values.exporterTLS.serverName }}
{{- $args = append $args (printf "-exporter-tls-server-name=%s" .Values.exporterTLS.serverName) -}}
{{- end }}
{{- end }}
{{/* Exporter Auth Headers */}}
{{- if .Values.exporterAuth.headers }}
{{- $args = append $args (printf "-exporter-auth-headers=%s" .Values.exporterAuth.headers) -}}
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
{{- if .Values.receiverTLS.enabled }}
- name: receiver-tls
  mountPath: /etc/tls/receiver
  readOnly: true
{{- if .Values.receiverTLS.caSecretName }}
- name: receiver-tls-ca
  mountPath: /etc/tls/receiver-ca
  readOnly: true
{{- end }}
{{- end }}
{{- if and .Values.receiverAuth.enabled .Values.receiverAuth.bearerTokenSecretName }}
- name: receiver-auth-token
  mountPath: /etc/auth/receiver
  readOnly: true
{{- end }}
{{- if and .Values.receiverAuth.enabled .Values.receiverAuth.basicAuthSecretName }}
- name: receiver-auth-basic
  mountPath: /etc/auth/receiver-basic
  readOnly: true
{{- end }}
{{- if .Values.exporterTLS.enabled }}
{{- if .Values.exporterTLS.secretName }}
- name: exporter-tls
  mountPath: /etc/tls/exporter
  readOnly: true
{{- end }}
{{- if .Values.exporterTLS.caSecretName }}
- name: exporter-tls-ca
  mountPath: /etc/tls/exporter-ca
  readOnly: true
{{- end }}
{{- end }}
{{- if .Values.exporterAuth.bearerTokenSecretName }}
- name: exporter-auth-token
  mountPath: /etc/auth/exporter
  readOnly: true
{{- end }}
{{- if .Values.exporterAuth.basicAuthSecretName }}
- name: exporter-auth-basic
  mountPath: /etc/auth/exporter-basic
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
{{- if .Values.receiverTLS.enabled }}
- name: receiver-tls
  secret:
    secretName: {{ .Values.receiverTLS.secretName }}
    items:
      - key: {{ .Values.receiverTLS.certKey }}
        path: tls.crt
      - key: {{ .Values.receiverTLS.keyKey }}
        path: tls.key
{{- if .Values.receiverTLS.caSecretName }}
- name: receiver-tls-ca
  secret:
    secretName: {{ .Values.receiverTLS.caSecretName }}
    items:
      - key: {{ .Values.receiverTLS.caKey }}
        path: ca.crt
{{- end }}
{{- end }}
{{- if and .Values.receiverAuth.enabled .Values.receiverAuth.bearerTokenSecretName }}
- name: receiver-auth-token
  secret:
    secretName: {{ .Values.receiverAuth.bearerTokenSecretName }}
{{- end }}
{{- if and .Values.receiverAuth.enabled .Values.receiverAuth.basicAuthSecretName }}
- name: receiver-auth-basic
  secret:
    secretName: {{ .Values.receiverAuth.basicAuthSecretName }}
{{- end }}
{{- if .Values.exporterTLS.enabled }}
{{- if .Values.exporterTLS.secretName }}
- name: exporter-tls
  secret:
    secretName: {{ .Values.exporterTLS.secretName }}
    items:
      - key: {{ .Values.exporterTLS.certKey }}
        path: tls.crt
      - key: {{ .Values.exporterTLS.keyKey }}
        path: tls.key
{{- end }}
{{- if .Values.exporterTLS.caSecretName }}
- name: exporter-tls-ca
  secret:
    secretName: {{ .Values.exporterTLS.caSecretName }}
    items:
      - key: {{ .Values.exporterTLS.caKey }}
        path: ca.crt
{{- end }}
{{- end }}
{{- if .Values.exporterAuth.bearerTokenSecretName }}
- name: exporter-auth-token
  secret:
    secretName: {{ .Values.exporterAuth.bearerTokenSecretName }}
{{- end }}
{{- if .Values.exporterAuth.basicAuthSecretName }}
- name: exporter-auth-basic
  secret:
    secretName: {{ .Values.exporterAuth.basicAuthSecretName }}
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
    env:
      {{- if and .Values.receiverAuth.enabled .Values.receiverAuth.bearerTokenSecretName }}
      - name: RECEIVER_AUTH_BEARER_TOKEN
        valueFrom:
          secretKeyRef:
            name: {{ .Values.receiverAuth.bearerTokenSecretName }}
            key: {{ .Values.receiverAuth.bearerTokenKey }}
      {{- end }}
      {{- if and .Values.receiverAuth.enabled .Values.receiverAuth.basicAuthSecretName }}
      - name: RECEIVER_AUTH_BASIC_USERNAME
        valueFrom:
          secretKeyRef:
            name: {{ .Values.receiverAuth.basicAuthSecretName }}
            key: {{ .Values.receiverAuth.usernameKey }}
      - name: RECEIVER_AUTH_BASIC_PASSWORD
        valueFrom:
          secretKeyRef:
            name: {{ .Values.receiverAuth.basicAuthSecretName }}
            key: {{ .Values.receiverAuth.passwordKey }}
      {{- end }}
      {{- if .Values.exporterAuth.bearerTokenSecretName }}
      - name: EXPORTER_AUTH_BEARER_TOKEN
        valueFrom:
          secretKeyRef:
            name: {{ .Values.exporterAuth.bearerTokenSecretName }}
            key: {{ .Values.exporterAuth.bearerTokenKey }}
      {{- end }}
      {{- if .Values.exporterAuth.basicAuthSecretName }}
      - name: EXPORTER_AUTH_BASIC_USERNAME
        valueFrom:
          secretKeyRef:
            name: {{ .Values.exporterAuth.basicAuthSecretName }}
            key: {{ .Values.exporterAuth.usernameKey }}
      - name: EXPORTER_AUTH_BASIC_PASSWORD
        valueFrom:
          secretKeyRef:
            name: {{ .Values.exporterAuth.basicAuthSecretName }}
            key: {{ .Values.exporterAuth.passwordKey }}
      {{- end }}
      {{- with .Values.env }}
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
