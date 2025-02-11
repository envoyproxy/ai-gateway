{{- define "type" -}}
{{- $type := . -}}
{{- if markdownShouldRenderType $type -}}

#### {{ $type.Name }}

{{ if $type.IsAlias }}**Underlying type:** {{ markdownRenderTypeLink $type.UnderlyingType  }}{{ end }}
{{ if $type.References }}
**Appears in:**
{{- range $type.SortedReferences }}
- {{ markdownRenderTypeLink . }}
{{- end }}
{{- end }}

{{ $type.Doc }}

{{ if $type.Members -}}

##### Fields

{{ if $type.GVK -}}
<ApiField
  name="apiVersion"
  type="String"
  required="true"
  description="We are on version <code>{{ $type.GVK.Group }}/{{ $type.GVK.Version }}</code> of the API."
/>

<ApiField
  name="kind"
  type="String"
  required="true"
  description="This is a <code>{{ $type.GVK.Kind }}</code> resource"
/>
{{- end }}

{{ range $type.Members -}}
{{- if not .Markers.notImplementedHide -}}
<ApiField
  name="{{ .Name }}"
  type="{{ markdownRenderType .Type }}"
  required="{{ if .Markers.optional }}false{{ else }}true{{ end }}"
  {{- if .Default }}
  defaultValue="{{ .Default }}"
  {{- end }}
  description="{{ template "type_members" . }}"
/>
{{- end }}
{{- end }}
{{- end }}

{{ if $type.EnumValues -}}
##### Possible Values

{{ range $type.EnumValues -}}
<ApiField
  name="{{ .Name }}"
  type="enum"
  required="false"
  description="{{ markdownRenderFieldDoc .Doc }}"
/>
{{- end }}
{{- end }}

{{- end }}
{{- end -}}
