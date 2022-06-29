package main

import (
	"fmt"
	"github.com/davecgh/go-spew/spew"
	"net/http"
	"net/url"
	"os"

	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"
	svix "github.com/svix/svix-webhooks/go"
)

const (
	defaultBind = ":8080"

	healthCheckPath = "/_healthcheck"

	svixToken  = "testsk_CSkhagouqu-JXgZznr35dG2TYTmsCPnb"
	svixServer = "https://api.eu.svix.com"
)

var Version = "v0.0"

var svixAppID = "app_2BEv2hBcE2ICiB6hq1QOVTVBWgF"

func main() {
	fmt.Println("version:", Version)

	logger := logrus.New()
	logger.SetFormatter(&logrus.JSONFormatter{})

	router := mux.NewRouter()
	router.Handle(healthCheckPath,
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			logger.Infof("health check OK")
			w.WriteHeader(http.StatusOK)
		}),
	)

	serverUrl, _ := url.Parse(svixServer)
	svixClient := svix.New(svixToken, &svix.SvixOptions{
		ServerUrl: serverUrl,
	})
	spew.Dump(svixClient)

	_ = svixClient.Application.Delete("app_2B9tnanuDzI0jaHJz7dACXZ57Hy")

	app, err := svixClient.Application.GetOrCreate(&svix.ApplicationIn{
		Name: "Formance Webhooks Application",
		Uid:  &svixAppID,
	})
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	spew.Dump(app)

	logger.Infof("starting http server on address: %s", defaultBind)
	if err := http.ListenAndServe(defaultBind, router); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
