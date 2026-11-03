package main

import (
	"embed"
	"fmt"
	"net/http"
)

//go:embed ui/index.html
var archivosUI embed.FS

// paginaIndex muestra y sirve la GUI embebida en el ejecutable
// (nada externo: funciona en un navegador si leerlo del disco).
func paginaIndex(w http.ResponseWriter, r *http.Request) {
	b, err := archivosUI.ReadFile("ui/index.html")
	if err != nil {
		http.Error(w, fmt.Sprintf("gui embebida falló: %v", err), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(b)
}
