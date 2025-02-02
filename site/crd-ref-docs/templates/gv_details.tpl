{{- define "gvDetails" -}}
{{- $gv := . -}}

<a name="{{ lower (replace $gv.GroupVersionString " " "-" ) }}"></a>
# {{ $gv.GroupVersionString }}

{{ $gv.Doc }}

{{- if $gv.Kinds  }}
## Resource Types
{{- range $gv.SortedKinds }}
- {{ markdownRenderTypeLink ($gv.TypeForKind .) }}
{{- end }}
{{ end }}

{{ range $gv.SortedTypes }}
{{ template "type" . }}
{{ end }}

[Back to Packages](#api_references)

{{- end -}}
