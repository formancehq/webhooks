package server

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/numary/go-libs/sharedlogging"
	"github.com/numary/webhooks-cloud/internal/storage"
	"github.com/numary/webhooks-cloud/internal/storage/mongodb"
	"github.com/spf13/cobra"
)

var (
	Version  = "dev"
	BindAddr = ":8080"
)

type API struct {
	Handler http.Handler
	Store   storage.Store

	ctx context.Context
	cl  context.CancelFunc
}

func Start(cmd *cobra.Command, args []string) error {
	sharedlogging.Infof("env: %+v", syscall.Environ())
	sharedlogging.Infof("app started with version: %s", Version)

	api := &API{}
	if err := api.Init(); err != nil {
		return err
	}

	return api.Run()
}

func (a *API) Init() error {
	var err error
	sharedlogging.Infof("initialize background context")
	a.ctx, a.cl = context.WithCancel(context.Background())
	a.cl = a.makeCancel(a.cl)
	defer func() {
		if err != nil {
			a.cl()
		}
	}()

	sharedlogging.Infof("initialize store")
	a.Store, err = mongodb.NewStore()
	if err != nil {
		return fmt.Errorf("mongodb.NewStore: %w", err)
	}

	sharedlogging.Infof("initialize http handler")
	a.Handler = NewWebhooksHandler(a.Store)

	return nil
}

func (a *API) Run() error {
	var err error
	errChan := make(chan error, 2)
	go func() {
		sharedlogging.Infof("http listening on %s", BindAddr)
		errChan <- http.ListenAndServe(BindAddr, a.Handler)
	}()
	go func() {
		c := make(chan os.Signal, 1)
		signal.Notify(c, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)
		errChan <- fmt.Errorf("%s", <-c)
	}()

	select {
	case err = <-errChan:
		a.cl()
	case <-a.ctx.Done():
	}

	return err
}

func (a *API) makeCancel(cl context.CancelFunc) context.CancelFunc {
	return func() {
		cl()
		if a.Store != nil {
			if err := a.Store.Close(); err != nil {
				sharedlogging.Errorf("API.Store.Close: %s", err)
			}
		}
	}
}
