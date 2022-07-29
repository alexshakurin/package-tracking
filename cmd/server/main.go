package main

import (
	"context"
	"fmt"
	"github.com/alexshakurin/package-tracking/server"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
	"os"
	"os/signal"
	"strings"
	"syscall"
)

const (
	defaultLogEnv      = "DEVELOPMENT"
	defaultHttpAddress = ":8080"
)

type envOptions struct {
	LogEnv  string
	Address string
}

func main() {
	os.Exit(start())
}

func start() int {

	opts := loadEnv()
	logger, err := createLogger(opts.LogEnv)
	if err != nil {
		fmt.Println("error setting up the logger: ", err)
		return 1
	}

	defer func() {
		_ = logger.Sync()
	}()

	s := server.New(server.Options{
		Log:     logger,
		Address: opts.Address,
	})

	var errGroup errgroup.Group

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	errGroup.Go(func() error {
		<-ctx.Done()
		if err := s.Stop(); err != nil {
			return err
		}

		return nil
	})

	// TODO: stop other activities if any

	if err := s.Start(); err != nil {
		logger.Error("error starting the server", zap.Error(err))
	}

	if err := errGroup.Wait(); err != nil {
		return 1
	}

	return 0
}

func loadEnv() envOptions {
	logEnv, ok := os.LookupEnv("LOG_ENV")
	if !ok {
		logEnv = defaultLogEnv
	}

	httpAddress, ok := os.LookupEnv("HTTP_ADDRESS")
	if !ok {
		httpAddress = defaultHttpAddress
	}

	return envOptions{
		LogEnv:  logEnv,
		Address: httpAddress,
	}
}

func createLogger(logEnv string) (*zap.Logger, error) {
	var logger *zap.Logger
	var err error
	switch strings.ToLower(logEnv) {
	case "development":
		logger, err = zap.NewDevelopment()
		break
	case "production":
		logger, err = zap.NewProduction()
		break
	default:
		logger = zap.NewNop()
	}

	return logger, err
}
