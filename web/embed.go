package web

import "embed"

//go:embed static templates i18n
var Assets embed.FS
