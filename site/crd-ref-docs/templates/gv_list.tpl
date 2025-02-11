{{- define "gvList" -}}
{{- $groupVersions := . -}}


---
id: api_references
title: API Reference
toc_min_heading_level: 2
toc_max_heading_level: 4
---

{{ range $groupVersions }}
{{ template "gvDetails" . }}
{{ end }}

{{- end -}}
