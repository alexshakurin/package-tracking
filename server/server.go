package server

import (
	"context"
	"errors"
	"fmt"
	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"
	"net/http"
	"time"
)

type Server struct {
	address string
	mux     chi.Router
	server  *http.Server
	log     *zap.Logger
}

type Options struct {
	Address  string
	RabbitMq string
	Log      *zap.Logger
}

func New(opts Options) *Server {

	var logger *zap.Logger
	if opts.Log == nil {
		logger = zap.NewNop()
	} else {
		logger = opts.Log
	}

	address := opts.Address
	mux := chi.NewMux()

	return &Server{
		address: address,
		mux:     mux,
		server: &http.Server{
			Addr:    address,
			Handler: mux,
		},
		log: logger,
	}
}

func (s *Server) Start() error {
	s.log.Info("starting server", zap.String("address", s.address))

	s.setupRoutes()

	if err := s.server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("error starting server: %w", err)
	}

	return nil
}

func (s *Server) Stop() error {

	s.log.Info("stopping server")
	// no particular reason - just 30 seconds
	const maxTimeout = 30
	ctx, stop := context.WithTimeout(context.Background(), maxTimeout*time.Second)
	defer stop()

	if err := s.server.Shutdown(ctx); err != nil {
		return fmt.Errorf("error stopping server: %w", err)
	}

	return nil
}
