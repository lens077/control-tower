{{/*
镜像引用：<registry>/<repository>:<tag>。tag 为空直接报错——
部署引用必须是不可变 tag（sha-<7位> 或发布版本），不允许静默落到 latest。
*/}}
{{- define "ct.image" -}}
{{- $tag := .image.tag | toString -}}
{{- if not $tag -}}{{- fail (printf "%s: image.tag 为空，必须是不可变 tag" .name) -}}{{- end -}}
{{- printf "%s/%s:%s" .registry .image.repository $tag -}}
{{- end -}}
