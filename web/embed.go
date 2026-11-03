package web

import "embed"

//go:embed *.html *.js *.css vendor/*
var FS embed.FS
