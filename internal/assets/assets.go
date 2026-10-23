// Package assets embeds reproducible self-hosted browser assets.
// npm run assets:build must run before compiling Go.
package assets

import "embed"

//go:embed app.css app.js htmx.min.js
var FS embed.FS
