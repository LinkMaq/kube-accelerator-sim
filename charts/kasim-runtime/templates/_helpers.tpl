{{- define "kasim-runtime.name" -}}
kasim-runtime
{{- end }}

{{- define "kasim-runtime.fullname" -}}
{{- printf "%s-%s" .Release.Name (include "kasim-runtime.name" .) | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "kasim-runtime.labels" -}}
app.kubernetes.io/name: {{ include "kasim-runtime.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version }}
simulation.kasim.io/runtime-contract: v1alpha1
{{- end }}

{{- define "kasim-runtime.selectorLabels" -}}
app.kubernetes.io/name: {{ include "kasim-runtime.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{- define "kasim-runtime.controllerImage" -}}
{{- $tag := default .Chart.AppVersion .Values.controller.image.tag -}}
{{- if ne $tag .Chart.AppVersion -}}
{{- fail (printf "controller.image.tag %q must match product version %q" $tag .Chart.AppVersion) -}}
{{- end -}}
{{- if .Values.controller.image.digest -}}
{{- printf "%s@%s" .Values.controller.image.repository .Values.controller.image.digest -}}
{{- else -}}
{{- printf "%s:%s" .Values.controller.image.repository $tag -}}
{{- end -}}
{{- end }}

{{- define "kasim-runtime.realNodeAffinity" -}}
nodeAffinity:
  requiredDuringSchedulingIgnoredDuringExecution:
    nodeSelectorTerms:
      - matchExpressions:
          - key: app.kubernetes.io/managed-by
            operator: NotIn
            values:
              - kube-accelerator-sim
          - key: simulation.kasim.io/instance-uid
            operator: DoesNotExist
{{- end }}

{{- define "kasim-runtime.assertOwnership" -}}
{{- $root := "kasim-runtime/v1alpha1" -}}
{{- $resources := list
  (dict "apiVersion" "apiextensions.k8s.io/v1" "kind" "CustomResourceDefinition" "name" "scenarioinstances.simulation.kasim.io")
  (dict "apiVersion" "apiextensions.k8s.io/v1" "kind" "CustomResourceDefinition" "name" "stages.kwok.x-k8s.io")
-}}
{{- range $resource := $resources -}}
  {{- $existing := lookup $resource.apiVersion $resource.kind "" $resource.name -}}
  {{- if $existing -}}
    {{- $actual := dig "metadata" "annotations" "simulation.kasim.io/ownership-root" "" $existing -}}
    {{- if ne $actual $root -}}
      {{- fail (printf "refusing to adopt incompatible %s/%s: ownership root %q, expected %q" $resource.kind $resource.name $actual $root) -}}
    {{- end -}}
  {{- end -}}
{{- end -}}
{{- end }}
