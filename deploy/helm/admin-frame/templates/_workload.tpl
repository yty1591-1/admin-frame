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
      {{- with $root.Values.podAnnotations }}
      annotations:
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
          {{- with $root.Values.env }}
          env:
            {{- toYaml . | nindent 12 }}
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
