{{- define "skael.fullname" -}}
{{- printf "%s-skael" .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "skael.labels" -}}
app.kubernetes.io/name: skael
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{- define "skael.databaseSecret" -}}
{{- default (printf "%s-database" (include "skael.fullname" .)) .Values.server.database.existingSecret -}}
{{- end -}}

{{- define "skael.keysSecret" -}}
{{- default (printf "%s-master-keys" (include "skael.fullname" .)) .Values.server.masterKeys.existingSecret -}}
{{- end -}}
