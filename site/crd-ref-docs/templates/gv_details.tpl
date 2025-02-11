{{- define "gvDetails" -}}
{{- $gv := . -}}

## {{ $gv.GroupVersionString }}

{{ $gv.Doc }}

{{- if $gv.Kinds }}
## Resource Kinds

### Available Kinds
{{- range $gv.SortedKinds }}
- {{ $gv.TypeForKind . | markdownRenderTypeLink }}
{{- end }}

### Kind Definitions
{{- range $gv.SortedKinds }}
{{- $type := $gv.TypeForKind . }}
{{ template "type" $type }}
{{- end }}
{{- end }}

{{- if $gv.Types }}
## Supporting Types

### Available Types
{{- range $gv.SortedTypes }}
{{- $type := . }}
{{- $isKind := false }}
{{- range $gv.Kinds }}
{{- if eq . $type.Name }}
{{- $isKind = true }}
{{- end }}
{{- end }}
{{- if not $isKind }}
- {{ markdownRenderTypeLink . }}
{{- end }}
{{- end }}

### Type Definitions
{{- range $gv.SortedTypes }}
{{- $type := . }}
{{- $isKind := false }}
{{- range $gv.Kinds }}
{{- if eq . $type.Name }}
{{- $isKind = true }}
{{- end }}
{{- end }}
{{- if not $isKind }}
{{ template "type" . }}
{{- end }}
{{- end }}
{{- end }}

{{- end -}}
