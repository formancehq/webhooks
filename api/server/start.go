package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"syscall"

	"github.com/numary/go-libs/sharedlogging"
	"github.com/numary/webhooks-cloud/cmd/constants"
	"github.com/numary/webhooks-cloud/internal/kafka"
	"github.com/numary/webhooks-cloud/internal/storage"
	"github.com/numary/webhooks-cloud/internal/storage/mongo"
	"github.com/numary/webhooks-cloud/internal/svix"
	kafkago "github.com/segmentio/kafka-go"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	svixgo "github.com/svix/svix-webhooks/go"
	"go.uber.org/fx"
)

func Start(*cobra.Command, []string) error {
	sharedlogging.Infof("env: %+v", syscall.Environ())

	app := fx.New(StartModule())
	app.Run()

	return nil
}

func StartModule() fx.Option {
	return fx.Module("webhooks-module",
		fx.Provide(
			mongo.NewConfigStore,
			svix.New,
			newConfigHandler,
			newHttpServeMux,
			newKafkaEngine,
		),
		fx.Invoke(registerConfigHandler, runKafkaEngine),
	)
}

func newHttpServeMux(lc fx.Lifecycle) *http.ServeMux {
	bindAddr := viper.GetString(constants.ServerHttpBindAddressFlag)
	if bindAddr == "" {
		bindAddr = constants.DefaultBindAddress
	}

	mux := http.NewServeMux()
	server := &http.Server{
		Addr:    bindAddr,
		Handler: mux,
	}

	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			sharedlogging.Infof(fmt.Sprintf("starting HTTP server on %s", bindAddr))
			go func() {
				if err := server.ListenAndServe(); err != nil &&
					!errors.Is(err, http.ErrServerClosed) {
					sharedlogging.Errorf("ListenAndServe: %s", err)
				}
			}()
			return nil
		},
		OnStop: func(ctx context.Context) error {
			sharedlogging.Infof("stopping HTTP server")
			return server.Shutdown(ctx)
		},
	})

	return mux
}

func newKafkaEngine(lc fx.Lifecycle, store storage.Store, svixClient *svixgo.Svix, svixAppId string) (*kafka.Engine, error) {
	cfg, err := kafka.NewKafkaReaderConfig()
	if err != nil {
		return nil, err
	}
	reader := kafkago.NewReader(cfg)

	lc.Append(fx.Hook{
		OnStop: func(ctx context.Context) error {
			return reader.Close()
		},
	})

	return kafka.NewEngine(reader, store, svixClient, svixAppId), nil
}

func registerConfigHandler(mux *http.ServeMux, h http.Handler) {
	mux.Handle("/", h)
}

func runKafkaEngine(e *kafka.Engine) {
	go func() {
		if _, _, err := e.Run(context.Background()); err != nil {
			sharedlogging.Errorf("Engine.Run: %s", err)
		}
	}()
}
