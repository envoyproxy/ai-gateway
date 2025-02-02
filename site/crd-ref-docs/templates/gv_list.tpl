{{- define "gvList" -}}
{{- $groupVersions := . -}}

---
id: api_references
title: API Reference
---

<a id="api_references"></a>
# Packages
{{- range $groupVersions }}
- [{{ .GroupVersionString }}](#{{ lower (replace .GroupVersionString " " "-" ) }})
{{- end }}

{{ range $groupVersions }}
{{ template "gvDetails" . }}
{{ end }}

{{- end -}}
