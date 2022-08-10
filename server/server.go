package server

import (
	"context"
	"errors"
	"fmt"
	"github.com/alexshakurin/package-tracking/messaging"
	"github.com/go-chi/chi/v5"
	"github.com/gorilla/websocket"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
	"net"
	"net/http"
	"time"
)

type Server struct {
	address  string
	mux      chi.Router
	server   *http.Server
	log      *zap.Logger
	upgrader *websocket.Upgrader
	conn     *messaging.RabbitMqConnection
}

type Options struct {
	Host     string
	Port     string
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

	address := net.JoinHostPort(opts.Host, opts.Port)
	mux := chi.NewMux()

	rabbitMq := messaging.Must(messaging.NewConnection(opts.RabbitMq, logger))

	return &Server{
		address: address,
		mux:     mux,
		server: &http.Server{
			Addr:    address,
			Handler: mux,
		},
		log:      logger,
		upgrader: &websocket.Upgrader{},
		conn:     rabbitMq,
	}
}

func (s *Server) Start(ctx context.Context) error {
	s.log.Info("starting server", zap.String("address", s.address))

	s.setupRoutes()

	s.conn.ListenForDisconnect(ctx)

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

	var errGroup errgroup.Group
	errGroup.Go(func() error {
		if err := s.conn.Close(ctx); err != nil {
			return fmt.Errorf("error closing rabbitmq connection: %w", err)
		}

		return nil
	})

	errGroup.Go(func() error {
		if err := s.server.Shutdown(ctx); err != nil {
			return fmt.Errorf("error stopping server: %w", err)
		}

		return nil
	})

	return errGroup.Wait()
}
