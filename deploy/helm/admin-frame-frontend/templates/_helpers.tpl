{{/*
命名与标签（INF-REL-03）：与后端 Chart 保持同一套 metadata 约定。
*/}}
{{- define "admin-frame-frontend.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "admin-frame-frontend.fullname" -}}
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

{{- define "admin-frame-frontend.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .root.Chart.Name .root.Chart.Version | replace "+" "_" | trunc 63 }}
{{ include "admin-frame-frontend.selectorLabels" . }}
app.kubernetes.io/version: {{ .root.Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .root.Release.Service }}
app.kubernetes.io/part-of: admin-frame
{{- end -}}

{{- define "admin-frame-frontend.selectorLabels" -}}
app.kubernetes.io/name: {{ include "admin-frame-frontend.name" .root }}
app.kubernetes.io/instance: {{ .root.Release.Name }}
app.kubernetes.io/component: {{ .component }}
{{- end -}}

{{/*
镜像引用：digest 优先于 tag；两者都为空时回退 Chart.AppVersion，禁止隐式 latest。
*/}}
{{- define "admin-frame-frontend.image" -}}
{{- $repo := required "workload.image.repository 必填" .workload.image.repository -}}
{{- $base := printf "%s/%s/%s" .root.Values.image.registry .root.Values.image.namespace $repo -}}
{{- if .workload.image.digest -}}
{{- printf "%s@%s" $base .workload.image.digest -}}
{{- else -}}
{{- $tag := default .root.Chart.AppVersion .workload.image.tag -}}
{{- printf "%s:%s" $base $tag -}}
{{- end -}}
{{- end -}}
