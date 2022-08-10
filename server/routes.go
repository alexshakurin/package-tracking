package server

import "github.com/alexshakurin/package-tracking/handlers"

func (s *Server) setupRoutes() {
	handlers.Health(s.mux)
	handlers.Web(s.mux)
	handlers.TrackApi(s.mux, s.upgrader, s.conn, s.log)
}
