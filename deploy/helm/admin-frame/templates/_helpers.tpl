{{/*
通用名称、标签与镜像解析。
*/}}
{{- define "admin-frame.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "admin-frame.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name (include "admin-frame.name" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}

{{- define "admin-frame.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .root.Chart.Name .root.Chart.Version | replace "+" "_" }}
app.kubernetes.io/name: {{ include "admin-frame.name" .root }}
app.kubernetes.io/instance: {{ .root.Release.Name }}
app.kubernetes.io/managed-by: {{ .root.Release.Service }}
app.kubernetes.io/version: {{ .root.Chart.AppVersion | quote }}
{{- with .component }}
app.kubernetes.io/component: {{ . }}
{{- end }}
{{- end -}}

{{- define "admin-frame.selectorLabels" -}}
app.kubernetes.io/name: {{ include "admin-frame.name" .root }}
app.kubernetes.io/instance: {{ .root.Release.Name }}
app.kubernetes.io/component: {{ .component }}
{{- end -}}

{{- define "admin-frame.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{- default (include "admin-frame.fullname" .) .Values.serviceAccount.name -}}
{{- else -}}
{{- default "default" .Values.serviceAccount.name -}}
{{- end -}}
{{- end -}}

{{- define "admin-frame.configMapName" -}}
{{- printf "%s-%s-config" (include "admin-frame.fullname" .root) .key -}}
{{- end -}}

{{/* 解析镜像：digest 优先；tag 空时回退 AppVersion；禁止 latest。 */}}
{{- define "admin-frame.image" -}}
{{- $root := .root -}}
{{- $w := .workload -}}
{{- $tag := $w.image.tag | default $root.Values.image.tag | default $root.Chart.AppVersion -}}
{{- $digest := $w.image.digest | default $root.Values.image.digest -}}
{{- if and (eq $tag "latest") (not $digest) -}}
{{- fail (printf "image tag must not be latest for %s; pin a semver/sha tag or set image.digest" $w.image.repository) -}}
{{- end -}}
{{- if $digest -}}
{{- printf "%s/%s/%s@%s" $root.Values.image.registry $root.Values.image.namespace $w.image.repository $digest -}}
{{- else -}}
{{- printf "%s/%s/%s:%s" $root.Values.image.registry $root.Values.image.namespace $w.image.repository $tag -}}
{{- end -}}
{{- end -}}

{{/* 渲染单个 workload 的应用配置；service/channel/addr/id 不允许被 overrides 覆盖。 */}}
{{- define "admin-frame.appConfig" -}}
{{- $root := .root -}}
{{- $key := .key -}}
{{- $w := index $root.Values.workloads $key -}}
{{- $cfg := deepCopy $root.Values.config -}}
{{- with $w.configOverrides -}}
{{- $cfg = mergeOverwrite $cfg . -}}
{{- end -}}
{{- $_ := set $cfg "service" $w.serviceName -}}
{{- $_ := set $cfg "channel" $w.channel -}}
{{- $_ := set $cfg "addr" (printf ":%d" (int $w.port)) -}}
{{- if $w.statefulset.enabled -}}
{{- $_ := set $cfg "id" (dict "node_id_source" "statefulset") -}}
{{- else -}}
{{- $_ := set $cfg "id" (dict "node_id_source" "config" "node_id" (int $w.nodeID)) -}}
{{- end -}}
{{- toYaml $cfg -}}
{{- end -}}
