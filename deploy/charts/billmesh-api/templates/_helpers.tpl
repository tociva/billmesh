{{- define "billmesh-api.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "billmesh-api.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name (include "billmesh-api.name" .) | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}

{{- define "billmesh-api.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" }}
app.kubernetes.io/name: {{ include "billmesh-api.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}

{{- define "billmesh-api.selectorLabels" -}}
app.kubernetes.io/name: {{ include "billmesh-api.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{- define "billmesh-api.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "billmesh-api.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- required "serviceAccount.name is required when serviceAccount.create is false" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{- define "billmesh-api.image" -}}
{{- printf "%s@%s" (required "image.repository is required" .Values.image.repository) (required "image.digest is required" .Values.image.digest) }}
{{- end }}

{{- define "billmesh-api.podVolumes" -}}
- name: tmp
  emptyDir:
    medium: Memory
    sizeLimit: 64Mi
{{- if .Values.secrets.postgresCa.secretName }}
- name: postgres-ca
  secret:
    secretName: {{ .Values.secrets.postgresCa.secretName }}
    defaultMode: 0440
    items:
      - key: {{ .Values.secrets.postgresCa.key }}
        path: ca.crt
{{- end }}
{{- if .Values.secrets.outboundCa.secretName }}
- name: outbound-ca
  secret:
    secretName: {{ .Values.secrets.outboundCa.secretName }}
    defaultMode: 0440
    items:
      - key: {{ .Values.secrets.outboundCa.key }}
        path: ca.crt
{{- end }}
{{- end }}

{{- define "billmesh-api.volumeMounts" -}}
- name: tmp
  mountPath: /tmp
{{- if .Values.secrets.postgresCa.secretName }}
- name: postgres-ca
  mountPath: {{ .Values.secrets.postgresCa.mountPath }}
  subPath: ca.crt
  readOnly: true
{{- end }}
{{- if .Values.secrets.outboundCa.secretName }}
- name: outbound-ca
  mountPath: {{ .Values.secrets.outboundCa.mountPath }}
  subPath: ca.crt
  readOnly: true
{{- end }}
{{- end }}
