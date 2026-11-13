{{/* 单个 workload 的 Deployment / StatefulSet。*/}}
{{- define "admin-frame.workload" -}}
{{- $root := .root -}}
{{- $key := .key -}}
{{- $w := index $root.Values.workloads $key -}}
{{- if $w.enabled -}}
{{- $stateful := $w.statefulset.enabled | default false -}}
apiVersion: apps/v1
kind: {{ if $stateful }}StatefulSet{{ else }}Deployment{{ end }}
metadata:
  name: {{ include "admin-frame.fullname" $root }}-{{ $key }}
  labels:
    {{- include "admin-frame.labels" (dict "root" $root "component" $key) | nindent 4 }}
spec:
  replicas: {{ $w.replicas }}
  {{- if $stateful }}
  serviceName: {{ include "admin-frame.fullname" $root }}-{{ $key }}-headless
  podManagementPolicy: Parallel
  {{- else }}
  strategy:
    type: RollingUpdate
    rollingUpdate:
      maxUnavailable: 0
      maxSurge: 1
  {{- end }}
  selector:
    matchLabels:
      {{- include "admin-frame.selectorLabels" (dict "root" $root "component" $key) | nindent 6 }}
  template:
    metadata:
      labels:
        {{- include "admin-frame.selectorLabels" (dict "root" $root "component" $key) | nindent 8 }}
        {{- with $root.Values.podLabels }}
        {{- toYaml . | nindent 8 }}
        {{- end }}
      annotations:
        # ConfigMap 内容变化时必须滚动 Pod：K8s 不会因挂载的 ConfigMap 更新而重建 Pod，
        # 否则 helm upgrade 显示 deployed，进程却仍在读旧配置（2026-11-05 OIDC issuer 演练实测）。
        checksum/config: {{ include "admin-frame.appConfig" (dict "root" $root "key" $key) | sha256sum | quote }}
        {{- with $root.Values.podAnnotations }}
        {{- toYaml . | nindent 8 }}
        {{- end }}
    spec:
      serviceAccountName: {{ include "admin-frame.serviceAccountName" $root }}
      {{- with $root.Values.imagePullSecrets }}
      imagePullSecrets:
        {{- toYaml . | nindent 8 }}
      {{- end }}
      securityContext:
        {{- toYaml $root.Values.podSecurityContext | nindent 8 }}
      terminationGracePeriodSeconds: 30
      containers:
        - name: {{ $key }}
          image: {{ include "admin-frame.image" (dict "root" $root "workload" $w) }}
          imagePullPolicy: {{ $root.Values.image.pullPolicy }}
          args: ["-config", "/etc/admin-frame/config.yaml"]
          ports:
            - name: http
              containerPort: {{ $w.port }}
              protocol: TCP
          securityContext:
            {{- toYaml $root.Values.securityContext | nindent 12 }}
          {{- if $w.probes.enabled }}
          startupProbe:
            httpGet:
              path: /healthz
              port: http
            failureThreshold: 30
            periodSeconds: 2
          livenessProbe:
            httpGet:
              path: /healthz
              port: http
            periodSeconds: 10
            timeoutSeconds: 3
            failureThreshold: 3
          readinessProbe:
            httpGet:
              path: /readyz
              port: http
            periodSeconds: 5
            timeoutSeconds: 3
            failureThreshold: 2
          {{- end }}
          resources:
            {{- toYaml $w.resources | nindent 12 }}
          {{- if or $root.Values.env $root.Values.identitySecrets.enabled (and $root.Values.configCenter.enabled $root.Values.configCenter.secretName) }}
          env:
            {{- with $root.Values.env }}
            {{- toYaml . | nindent 12 }}
            {{- end }}
            {{- if $root.Values.identitySecrets.enabled }}
            - name: ADMIN_FRAME_IDENTITY_HASH_KEY
              valueFrom:
                secretKeyRef:
                  name: {{ $root.Values.identitySecrets.secretName }}
                  key: {{ $root.Values.identitySecrets.hashKeyKey }}
            - name: ADMIN_FRAME_IDENTITY_MFA_SECRET_KEY
              valueFrom:
                secretKeyRef:
                  name: {{ $root.Values.identitySecrets.secretName }}
                  key: {{ $root.Values.identitySecrets.mfaSecretKey }}
            {{- end }}
            {{- if and $root.Values.configCenter.enabled $root.Values.configCenter.secretName }}
            {{- with $root.Values.configCenter.snapshotFiles }}
            - name: CONFIGCENTER_SNAPSHOT_FILES
              value: "{{- range $i, $file := splitList "," . }}{{ if $i }},{{ end }}{{ $root.Values.configCenter.mountPath }}/{{ trim $file }}{{- end }}"
            {{- end }}
            - name: CONFIGCENTER_ENABLED_NAMESPACES
              value: {{ $root.Values.configCenter.enabledNamespaces | quote }}
            {{- if $root.Values.configCenter.allowMissingConfig }}
            - name: CONFIGCENTER_ALLOW_MISSING_CONFIG
              value: "1"
            {{- end }}
            {{- if $root.Values.configCenter.publicKeys.secretName }}
            - name: CONFIGCENTER_SNAPSHOT_PUBLIC_KEYS
              valueFrom:
                secretKeyRef:
                  name: {{ $root.Values.configCenter.publicKeys.secretName }}
                  key: {{ $root.Values.configCenter.publicKeys.key }}
            {{- end }}
            {{- end }}
          {{- end }}
          {{- with $root.Values.envFrom }}
          envFrom:
            {{- toYaml . | nindent 12 }}
          {{- end }}
          volumeMounts:
            - name: config
              mountPath: /etc/admin-frame
              readOnly: true
            - name: tmp
              mountPath: /tmp
            {{- if $root.Values.jwtPublicKey.enabled }}
            - name: jwt-public-key
              mountPath: /etc/admin-frame/jwt
              readOnly: true
            {{- end }}
            {{- if (($root.Values.secretMounts | default dict).enabled) }}
            - name: app-secrets
              mountPath: {{ $root.Values.secretMounts.mountPath }}
              readOnly: true
            {{- end }}
            {{- if and $root.Values.configCenter.enabled $root.Values.configCenter.secretName }}
            - name: configcenter-snapshots
              mountPath: {{ $root.Values.configCenter.mountPath }}
              readOnly: true
            {{- end }}
      volumes:
        - name: config
          configMap:
            name: {{ include "admin-frame.configMapName" (dict "root" $root "key" $key) }}
        - name: tmp
          emptyDir: {}
        {{- if $root.Values.jwtPublicKey.enabled }}
        - name: jwt-public-key
          secret:
            secretName: {{ $root.Values.jwtPublicKey.secretName }}
            items:
              - key: {{ $root.Values.jwtPublicKey.key }}
                path: public.pem
              {{- if (($root.Values.jwtPrivateKey | default dict).enabled) }}
              - key: {{ $root.Values.jwtPrivateKey.key }}
                path: private.pem
              {{- end }}
        {{- end }}
        {{- if (($root.Values.secretMounts | default dict).enabled) }}
        # ENT-07：多个 Secret 投影到同一目录，文件名即密钥逻辑名（db_dsn 等）。
        - name: app-secrets
          projected:
            defaultMode: 0400
            sources:
              {{- range $root.Values.secretMounts.sources }}
              - secret:
                  name: {{ .secretName }}
                  {{- if .optional }}
                  optional: true
                  {{- end }}
                  items:
                    {{- toYaml .items | nindent 20 }}
              {{- end }}
        {{- end }}
        {{- if and $root.Values.configCenter.enabled $root.Values.configCenter.secretName }}
        # 配置中心签名快照；Secret 缺失时 Pod 仍可启动并降级为文件配置。
        - name: configcenter-snapshots
          secret:
            secretName: {{ $root.Values.configCenter.secretName }}
            optional: true
        {{- end }}
      topologySpreadConstraints:
        - maxSkew: 1
          topologyKey: kubernetes.io/hostname
          whenUnsatisfiable: ScheduleAnyway
          labelSelector:
            matchLabels:
              {{- include "admin-frame.selectorLabels" (dict "root" $root "component" $key) | nindent 14 }}
---
{{- end }}
{{- end }}
