package web

import "embed"

//go:embed *.html *.js *.css *.svg vendor/*
var FS embed.FS
