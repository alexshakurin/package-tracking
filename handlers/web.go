package handlers

import (
	"github.com/go-chi/chi/v5"
	"net/http"
)

func Web(mux chi.Router) {
	fs := http.FileServer(http.Dir("web"))
	mux.Handle("/web/*", http.StripPrefix("/web/", fs))
}
