package web

import (
	"embed"
	"fmt"
	"io/fs"
)

//go:embed static/*
var embeddedFS embed.FS

// StaticFS retorna o sub-filesystem com a raiz em 'static'
func StaticFS() (fs.FS, error) {
	sub, err := fs.Sub(embeddedFS, "static")
	if err != nil {
		return nil, fmt.Errorf("erro ao montar sub-filesystem estático: %w", err)
	}
	return sub, nil
}
