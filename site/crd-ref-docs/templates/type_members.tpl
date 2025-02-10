{{- define "type_members" -}}
{{- $field := . -}}
{{- if eq $field.Name "config" -}}
Refer to Kubernetes API documentation for fields of `config`.
{{- else -}}
{{ markdownRenderFieldDoc $field.Doc | replace "\"" "`" }}
{{- end -}}
{{- end -}}
