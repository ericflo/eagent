// Package webstatic shares the actual local editor assets with portable pages.
package webstatic

import "embed"

//go:embed *.html *.js *.css
var Files embed.FS
