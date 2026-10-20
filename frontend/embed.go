package frontend

import "embed"

//go:embed *.html *.css *.js *.svg *.png
var Files embed.FS
