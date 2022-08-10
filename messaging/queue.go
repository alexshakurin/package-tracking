package messaging

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/alexshakurin/package-tracking/model"
	"github.com/rabbitmq/amqp091-go"
	"go.uber.org/zap"
	"math/rand"
	"sync/atomic"
	"time"
)

const (
	exchangeName = "package_status"
)

type RabbitMqConnection struct {
	ch  *atomic.Pointer[amqp091.Channel]
	log *zap.Logger
	url string
}

var (
	ErrNotConnected = errors.New("not connected to rabbitmq")
)

func NewConnection(url string, log *zap.Logger) (*RabbitMqConnection, error) {

	ch, err := newRabbitMqConn(url)
	if err != nil {
		return nil, err
	}

	chP := atomic.Pointer[amqp091.Channel]{}
	chP.Swap(ch)

	res := &RabbitMqConnection{ch: &chP, log: log, url: url}

	return res, nil
}

func (q *RabbitMqConnection) Close(ctx context.Context) error {

	curCh := q.ch.Load()
	if curCh == nil {
		return ErrNotConnected
	}

	closeChan := make(chan error)
	go func() {
		e := curCh.Close()
		if !errors.Is(amqp091.ErrClosed, e) {
			closeChan <- e
		} else {
			closeChan <- nil
		}

		close(closeChan)
	}()

	select {
	case err := <-closeChan:
		return err
	case <-ctx.Done():
		return nil
	}
}

func (q *RabbitMqConnection) ListenForDisconnect(ctx context.Context) {
	curCh := q.ch.Load()
	if curCh == nil {
		// already closed
		return
	}

	go listenDisconnect(ctx, q)
}

func Must(conn *RabbitMqConnection, err error) *RabbitMqConnection {
	if err != nil {
		panic(err)
	}

	return conn
}

func (q *RabbitMqConnection) PublishStatus(ctx context.Context, status model.PackageStatus) error {
	curCh := q.ch.Load()
	if curCh == nil {
		return ErrNotConnected
	}

	q.log.Info("publish: start", zap.String("packageid", status.Id))

	j, err := json.Marshal(status)
	if err != nil {
		return fmt.Errorf("publish: json marshal error: %w", err)
	}

	err = curCh.PublishWithContext(ctx,
		exchangeName,
		status.Id,
		false,
		false,
		amqp091.Publishing{
			ContentType: "application/json",
			Body:        j,
		})

	if err != nil {
		return fmt.Errorf("publish: queue error: %w", err)
	}

	q.log.Info("publish: done", zap.String("packageid", status.Id))
	return nil
}

func (q *RabbitMqConnection) Listen(ctx context.Context, packageId string) (<-chan model.PackageStatus, error) {
	curCh := q.ch.Load()
	if curCh == nil {
		return nil, ErrNotConnected
	}

	number := rand.Int31n(5)
	if number == 4 {
		return nil, fmt.Errorf("test error")
	}

	queue, err := curCh.QueueDeclare("",
		false,
		false,
		true,
		false,
		nil)

	if err != nil {
		return nil, fmt.Errorf("listen: unable to declare a queue: %w", err)
	}

	err = curCh.QueueBind(queue.Name,
		packageId,
		exchangeName,
		false,
		nil)

	if err != nil {
		return nil, fmt.Errorf("listen: unable to bind a queue: %w", err)
	}

	msgs, err := curCh.Consume(queue.Name,
		"",
		true, //in our scenario it's OK to auto acknowledge delivery
		true,
		false,
		false,
		nil)

	if err != nil {
		return nil, fmt.Errorf("listen: unable to start consuming: %w", err)
	}

	return notifyStatus(ctx, msgs, packageId, q.log), nil
}

func newRabbitMqConn(url string) (*amqp091.Channel, error) {
	conn, err := amqp091.Dial(url)
	if err != nil {
		return nil, fmt.Errorf("error connecting to rabbitmq: %w", err)
	}

	ch, err := conn.Channel()
	if err != nil {
		return nil, fmt.Errorf("error opening channel: %w", err)
	}

	err = ch.ExchangeDeclare(exchangeName,
		"direct",
		true,
		false,
		false,
		false,
		nil)

	if err != nil {
		return nil, fmt.Errorf("error creating exchange: %w", err)
	}

	return ch, nil
}

func notifyStatus(ctx context.Context, msgs <-chan amqp091.Delivery, packageId string, log *zap.Logger) <-chan model.PackageStatus {
	c := make(chan model.PackageStatus)

	go copyStatus(ctx, msgs, c, packageId, log)

	return c
}

func copyStatus(ctx context.Context,
	fromChan <-chan amqp091.Delivery,
	toChan chan<- model.PackageStatus,
	packageId string,
	log *zap.Logger) {

	last := time.Now()

	defer func() {
		close(toChan)
		log.Info("stopping copy status", zap.String("packageid", packageId))
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case d, ok := <-fromChan:
			log.Info("copyStatus: got a message", zap.Bool("ok", ok))
			if !ok {
				return
			}

			var status model.PackageStatus
			err := json.Unmarshal(d.Body, &status)
			if err != nil {
				log.Error("copyStatus: json unmarshal error", zap.Error(err))
				return
			}

			if status.LastModified.After(last) {
				// make sure statuses will be published ordered by time
				// this is done to prevent lost messages from being sent to the user
				last = status.LastModified
				toChan <- status
			} else {
				log.Debug("copyStatus: skipping", zap.String("packageid", packageId))
			}
		}
	}
}

func listenDisconnect(ctx context.Context, conn *RabbitMqConnection) {
	for {
		curCh := conn.ch.Load()
		if curCh != nil {
			errChan := curCh.NotifyClose(make(chan *amqp091.Error))
			select {
			case <-ctx.Done():
				return
			case rErr := <-errChan:
				conn.log.Info("lost connection to rabbit", zap.String("rabbit_err", rErr.Error()))
				reconnect(ctx, conn)
			}
		}
	}
}

func reconnect(ctx context.Context, conn *RabbitMqConnection) {
	conn.ch.Swap(nil)
	connected := false
	newConnCh := retryConnect(ctx, conn.url, conn.log)

	started := time.Now()
	for !connected {
		newConn, ok := <-newConnCh
		if ok && newConn != nil {
			connected = true
			ended := time.Now()
			elapsed := ended.Sub(started)
			conn.log.Info("reconnected to rabbitmq", zap.Duration("reconnect", elapsed))
			conn.ch.Swap(newConn)
		}
	}
}

func retryConnect(ctx context.Context, url string, log *zap.Logger) chan *amqp091.Channel {
	connChan := make(chan *amqp091.Channel)
	go func(ctx context.Context) {
		done := false
		for !done {
			log.Debug("retryConnect: starting connect sequence")

			timeoutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)

			finish := func() {
				cancel()
				close(connChan)
				done = true
			}

			select {
			case <-ctx.Done():
				log.Debug("retryConnect: app is shutting down - cancelling")
				finish()
			case <-timeoutCtx.Done():
				log.Debug("retryConnect: timeout. checking rabbitmq status")
				cancel()
				newConn, err := newRabbitMqConn(url)
				if err == nil && newConn != nil {
					log.Debug("retryConnect: rabbitmq is back online")
					connChan <- newConn
					finish()
				} else if err != nil {
					log.Debug("retryConnect: rabbitmq is still offline", zap.Error(err))
				} else {
					log.Debug("retryConnect: connection not acquired")
				}
			}
		}
	}(ctx)

	return connChan
}
