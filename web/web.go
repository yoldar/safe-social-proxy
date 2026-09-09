// Package web embeds the portal and blocked-page templates.
package web

import "embed"

//go:embed portal.html blocked.html yt.html
var FS embed.FS
