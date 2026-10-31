// Package ui embeds the built React SPA (ui/dist, produced by `npm run build`) into the
// scrapy binary so it ships as a single deployable artifact.
package ui

import "embed"

//go:embed all:dist
var DistFS embed.FS
