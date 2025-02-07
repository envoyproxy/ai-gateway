{{- define "type" -}}
{{- $type := . -}}
{{- if markdownShouldRenderType $type -}}

### {{ $type.Name }}

{{ if $type.IsAlias }}_Underlying type:_ _{{ markdownRenderTypeLink $type.UnderlyingType  }}_{{ end }}

{{ $type.Doc }}

{{ if $type.References -}}
_Appears in:_
{{- range $type.SortedReferences }}
- {{ markdownRenderTypeLink . }}
{{- end }}
{{- end }}

{{ if $type.Members -}}
{{ if $type.GVK -}}
- **apiVersion**
  - **Type:** _string_
  - **Value:** `{{ $type.GVK.Group }}/{{ $type.GVK.Version }}`
- **kind**
  - **Type:** _string_
  - **Value:** `{{ $type.GVK.Kind }}`
{{ end -}}
{{ range $type.Members -}}
{{- with .Markers.notImplementedHide -}}
{{- else -}}
- **{{ .Name }}**
  - **Type:** _{{ markdownRenderType .Type }}_
  - **Required:** {{ if .Markers.optional }}No{{ else }}Yes{{ end }}
  {{- if .Doc }}
  - **Description:** {{ .Doc }}
  {{- end }}
{{ end -}}
{{- end -}}
{{- end -}}

{{ if $type.EnumValues -}}
| Value | Description |
| ----- | ----------- |
{{ range $type.EnumValues -}}
| `{{ .Name }}` | {{ markdownRenderFieldDoc .Doc }} |
{{ end -}}
{{- end -}}

{{- end -}}
{{- end -}}
