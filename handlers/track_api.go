package handlers

import (
	"context"
	"encoding/json"
	"github.com/alexshakurin/package-tracking/model"
	"github.com/go-chi/chi/v5"
	"github.com/gorilla/websocket"
	"go.uber.org/zap"
	"io/ioutil"
	"net/http"
	"time"
)

type packageTracker interface {
	Listen(ctx context.Context, packageId string) (<-chan model.PackageStatus, error)
	PublishStatus(ctx context.Context, status model.PackageStatus) error
}

func TrackApi(mux chi.Router, up *websocket.Upgrader, tracker packageTracker, log *zap.Logger) {
	mux.Get("/packages/track/{id}", func(rw http.ResponseWriter, r *http.Request) {
		packageId := chi.URLParam(r, "id")

		if packageId == "" {
			rw.WriteHeader(http.StatusBadRequest)
			return
		}

		wsConn, err := up.Upgrade(rw, r, nil)

		if err != nil {
			log.Info("unable to upgrade connection to ws", zap.Error(err))
			rw.WriteHeader(http.StatusBadGateway)
			return
		}
		defer wsConn.Close()

		ctx, cancel := context.WithCancel(r.Context())
		go func() {
			// goroutine quits when underlying http connection is closed
			// because wsConn.ReadMessage will return an error
			_, _, err = wsConn.ReadMessage()
			if err != nil {
				cancel()
			}
		}()

		defer cancel()

		statusChan, err := tracker.Listen(ctx, packageId)
		if err != nil {
			log.Info("unable to subscribe to package status tracker", zap.String("packageid", packageId),
				zap.Error(err))
			return
		} else {
			log.Info("subscribe OK", zap.String("packageid", packageId))
		}

		for exit := false; !exit; {
			select {
			case <-ctx.Done():
				exit = true
			case status, ok := <-statusChan:
				if ok {
					err := wsConn.WriteJSON(status)
					if err != nil {
						log.Error("error sending data to ws", zap.Error(err))
						exit = true
					}
				} else {
					exit = true
				}
			}
		}
	})

	mux.Post("/packages/track/{id}", func(rw http.ResponseWriter, r *http.Request) {
		packageId := chi.URLParam(r, "id")
		if packageId == "" {
			rw.WriteHeader(http.StatusBadRequest)
			return
		}

		var err error
		defer func() {
			err = r.Body.Close()
			if err != nil {
				log.Info("error closing request body", zap.String("packageid", packageId),
					zap.Error(err))
			}
		}()

		bytes, err := ioutil.ReadAll(r.Body)
		if err != nil {
			log.Info("error reading body", zap.String("packageid", packageId),
				zap.Error(err))
			rw.WriteHeader(http.StatusBadRequest)
			return
		}

		var newStatus model.PackageStatus
		err = json.Unmarshal(bytes, &newStatus)
		if err != nil {
			log.Error("error parsing status from request", zap.String("packageid", packageId),
				zap.Error(err))
			rw.WriteHeader(http.StatusBadRequest)
			return
		}

		var zeroTime time.Time
		if newStatus.Id == "" || newStatus.Status == "" || newStatus.LastModified == zeroTime {
			rw.WriteHeader(http.StatusBadRequest)
			return
		}

		err = tracker.PublishStatus(r.Context(), newStatus)
		if err != nil {
			log.Error("error publishing status", zap.String("packageid", packageId),
				zap.Error(err))
			rw.WriteHeader(http.StatusBadGateway)
		}
	})
}
