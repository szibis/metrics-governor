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
Generate container arguments (uses config file)
*/}}
{{- define "metrics-governor.args" -}}
{{- $args := list -}}
{{- $args = append $args "-config=/etc/metrics-governor/config.yaml" -}}
{{- if .Values.limits.enabled }}
{{- $args = append $args "-limits-config=/etc/metrics-governor/limits.yaml" -}}
{{- end }}
{{/* Optional CLI overrides for operational flexibility */}}
{{- if .Values.config.overrides }}
{{- if .Values.config.overrides.grpcListen }}
{{- $args = append $args (printf "-grpc-listen=%s" .Values.config.overrides.grpcListen) -}}
{{- end }}
{{- if .Values.config.overrides.httpListen }}
{{- $args = append $args (printf "-http-listen=%s" .Values.config.overrides.httpListen) -}}
{{- end }}
{{- if .Values.config.overrides.statsAddr }}
{{- $args = append $args (printf "-stats-addr=%s" .Values.config.overrides.statsAddr) -}}
{{- end }}
{{- if .Values.config.overrides.exporterEndpoint }}
{{- $args = append $args (printf "-exporter-endpoint=%s" .Values.config.overrides.exporterEndpoint) -}}
{{- end }}
{{- end }}
{{- range .Values.config.extraArgs }}
{{- $args = append $args . -}}
{{- end }}
{{- toYaml $args }}
{{- end }}

{{/*
Generate config YAML from values
*/}}
{{- define "metrics-governor.configYAML" -}}
receiver:
  grpc:
    address: {{ .Values.config.grpcListen | quote }}
  http:
    address: {{ .Values.config.httpListen | quote }}
    {{- if .Values.receiverHTTPServer }}
    server:
      max_request_body_size: {{ .Values.receiverHTTPServer.maxRequestBodySize | default 0 }}
      read_timeout: {{ .Values.receiverHTTPServer.readTimeout | default "0s" | quote }}
      read_header_timeout: {{ .Values.receiverHTTPServer.readHeaderTimeout | default "1m" | quote }}
      write_timeout: {{ .Values.receiverHTTPServer.writeTimeout | default "30s" | quote }}
      idle_timeout: {{ .Values.receiverHTTPServer.idleTimeout | default "1m" | quote }}
      keep_alives_enabled: {{ .Values.receiverHTTPServer.keepAlivesEnabled | default true }}
    {{- end }}
  {{- if .Values.receiverTLS.enabled }}
  tls:
    enabled: true
    cert_file: /etc/tls/receiver/tls.crt
    key_file: /etc/tls/receiver/tls.key
    {{- if .Values.receiverTLS.caSecretName }}
    ca_file: /etc/tls/receiver-ca/ca.crt
    {{- end }}
    client_auth: {{ .Values.receiverTLS.clientAuth | default false }}
  {{- end }}
  {{- if .Values.receiverAuth.enabled }}
  auth:
    enabled: true
  {{- end }}

exporter:
  endpoint: {{ .Values.config.exporterEndpoint | quote }}
  protocol: {{ .Values.config.exporterProtocol | quote }}
  insecure: {{ .Values.config.exporterInsecure }}
  timeout: {{ .Values.config.exporterTimeout | quote }}
  {{- if .Values.exporterTLS.enabled }}
  tls:
    enabled: true
    {{- if .Values.exporterTLS.secretName }}
    cert_file: /etc/tls/exporter/tls.crt
    key_file: /etc/tls/exporter/tls.key
    {{- end }}
    {{- if .Values.exporterTLS.caSecretName }}
    ca_file: /etc/tls/exporter-ca/ca.crt
    {{- end }}
    skip_verify: {{ .Values.exporterTLS.insecureSkipVerify | default false }}
    {{- if .Values.exporterTLS.serverName }}
    server_name: {{ .Values.exporterTLS.serverName | quote }}
    {{- end }}
  {{- end }}
  {{- if or .Values.exporterAuth.headers .Values.exporterAuth.bearerTokenSecretName .Values.exporterAuth.basicAuthSecretName }}
  auth:
    {{- if .Values.exporterAuth.headers }}
    headers:
      {{- range $key, $value := .Values.exporterAuth.headers }}
      {{ $key }}: {{ $value | quote }}
      {{- end }}
    {{- end }}
  {{- end }}
  {{- if .Values.exporterCompression }}
  compression:
    type: {{ .Values.exporterCompression.type | default "none" | quote }}
    level: {{ .Values.exporterCompression.level | default 0 }}
  {{- end }}
  {{- if .Values.exporterHTTPClient }}
  http_client:
    max_idle_conns: {{ .Values.exporterHTTPClient.maxIdleConns | default 100 }}
    max_idle_conns_per_host: {{ .Values.exporterHTTPClient.maxIdleConnsPerHost | default 100 }}
    max_conns_per_host: {{ .Values.exporterHTTPClient.maxConnsPerHost | default 0 }}
    idle_conn_timeout: {{ .Values.exporterHTTPClient.idleConnTimeout | default "90s" | quote }}
    disable_keep_alives: {{ .Values.exporterHTTPClient.disableKeepAlives | default false }}
    force_http2: {{ .Values.exporterHTTPClient.forceHTTP2 | default false }}
    http2_read_idle_timeout: {{ .Values.exporterHTTPClient.http2ReadIdleTimeout | default "0s" | quote }}
    http2_ping_timeout: {{ .Values.exporterHTTPClient.http2PingTimeout | default "0s" | quote }}
  {{- end }}
  {{- if .Values.queue }}
  queue:
    enabled: {{ .Values.queue.enabled | default false }}
    path: {{ .Values.queue.path | default "/data/queue" | quote }}
    max_size: {{ .Values.queue.maxSize | default 10000 }}
    max_bytes: {{ .Values.queue.maxBytes | default 1073741824 }}
    retry_interval: {{ .Values.queue.retryInterval | default "5s" | quote }}
    max_retry_delay: {{ .Values.queue.maxRetryDelay | default "5m" | quote }}
    full_behavior: {{ .Values.queue.fullBehavior | default "drop_oldest" | quote }}
    target_utilization: {{ .Values.queue.targetUtilization | default 0.85 }}
    adaptive_enabled: {{ .Values.queue.adaptiveEnabled | default true }}
    compact_threshold: {{ .Values.queue.compactThreshold | default 0.5 }}
  {{- end }}

buffer:
  size: {{ .Values.config.bufferSize }}
  batch_size: {{ .Values.config.batchSize }}
  flush_interval: {{ .Values.config.flushInterval | quote }}

stats:
  address: {{ .Values.config.statsAddr | quote }}
  {{- if .Values.config.statsLabels }}
  labels:
    {{- range (splitList "," .Values.config.statsLabels) }}
    - {{ . | trim | quote }}
    {{- end }}
  {{- end }}

limits:
  dry_run: {{ .Values.config.limitsDryRun }}
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
- name: config
  mountPath: /etc/metrics-governor/config.yaml
  subPath: config.yaml
  readOnly: true
{{- if .Values.limits.enabled }}
- name: limits-config
  mountPath: /etc/metrics-governor/limits.yaml
  subPath: limits.yaml
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
{{- else if .Values.queue.enabled }}
- name: queue-data
  mountPath: /data/queue
{{- end }}
{{- with .Values.extraVolumeMounts }}
{{ toYaml . }}
{{- end }}
{{- end }}

{{/*
Volumes
*/}}
{{- define "metrics-governor.volumes" -}}
- name: config
  configMap:
    name: {{ include "metrics-governor.fullname" . }}-config
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
{{- if and .Values.queue.enabled (not (and (eq .Values.kind "statefulset") .Values.persistence.enabled)) }}
- name: queue-data
  emptyDir: {}
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
