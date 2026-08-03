{{- define "seev.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "seev.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name (include "seev.name" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}

{{- define "seev.labels" -}}
app.kubernetes.io/name: {{ include "seev.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/part-of: seev
{{- end -}}

{{- define "seev.selectorLabels" -}}
app.kubernetes.io/name: {{ include "seev.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "seev.appNamespace" -}}{{ .Values.global.appNamespace }}{{- end -}}
{{- define "seev.dataNamespace" -}}{{ .Values.global.dataNamespace }}{{- end -}}
{{- define "seev.egressNamespace" -}}{{ .Values.global.egressNamespace }}{{- end -}}
{{- define "seev.edgeNamespace" -}}{{ .Values.global.edgeNamespace }}{{- end -}}

{{- define "seev.serviceImage" -}}
{{- $root := index . 0 -}}
{{- $spec := index . 1 -}}
{{- $repository := default $root.Values.global.image.repository $root.Values.image.repository -}}
{{- $tag := default $root.Values.global.image.tag $root.Values.image.tag -}}
{{- $digest := default (default $root.Values.global.image.digest $root.Values.image.digest) $spec.digest -}}
{{- if $digest -}}
{{- printf "%s/%s@%s" $repository $spec.image $digest -}}
{{- else -}}
{{- printf "%s/%s:%s" $repository $spec.image $tag -}}
{{- end -}}
{{- end -}}

{{- define "seev.migrationImage" -}}
{{- $repository := .Values.migrations.image.repository -}}
{{- $tag := .Values.migrations.image.tag -}}
{{- $digest := .Values.migrations.image.digest -}}
{{- if $digest -}}
{{- printf "%s@%s" $repository $digest -}}
{{- else -}}
{{- printf "%s:%s" $repository $tag -}}
{{- end -}}
{{- end -}}
