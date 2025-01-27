{{- define "gvList" -}}
{{- $groupVersions := . -}}


---
id: api_references
title: API Reference
---

## Packages
{{- range $groupVersions }}
- {{ markdownRenderGVLink . }}
{{- end }}

{{ range $groupVersions }}
{{ template "gvDetails" . }}
{{ end }}

{{- end -}}
