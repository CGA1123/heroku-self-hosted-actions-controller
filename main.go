package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/google/go-github/v38/github"
	"github.com/gorilla/handlers"
	"github.com/gorilla/mux"
	heroku "github.com/heroku/heroku-go/v5"
	"golang.org/x/oauth2"
)

const (
	GitHubToken  = "GITHUB_TOKEN" // requires admin:org token
	GitHubOrg    = "GITHUB_ORG"
	GitHubSecret = "GITHUB_SECRET"
	HerokuToken  = "X_HEROKU_TOKEN"
	HerokuLogin  = "X_HEROKU_LOGIN"
	HerokuApp    = "X_HEROKU_APP"
)

type WorkflowJob struct {
	github.WorkflowJob

	Labels []string `json:"labels,omitempty"`
}

type WorkflowJobEvent struct {
	Action       string               `json:"action"`
	WorkflowJob  *WorkflowJob         `json:"workflow_job,omitempty"`
	Organization *github.Organization `json:"organization,omitempty"`
	Repository   *github.Repository   `json:"repository,omitempty"`
	Sender       *github.User         `json:"sender,omitempty"`
}

type tokenCache struct {
	m     sync.Mutex
	token *github.RegistrationToken
}

func (t *tokenCache) Get(ctx context.Context, g *github.Client, org string) (string, error) {
	t.m.Lock()
	defer t.m.Unlock()

	if t.token == nil || t.token.GetExpiresAt().Before(time.Now().Add(5*time.Minute)) {
		token, _, err := g.Actions.CreateOrganizationRegistrationToken(ctx, org)
		if err != nil {
			return "", err
		}

		log.Printf("got token expiring at %v", token.GetExpiresAt())

		t.token = token
	}

	return *t.token.Token, nil
}

func main() {
	port, ok := os.LookupEnv("PORT")
	if !ok {
		port = "1123"
	}

	org := os.Getenv(GitHubOrg)
	app := os.Getenv(HerokuApp)
	secret := os.Getenv(GitHubSecret)
	h := heroku.NewService(
		&http.Client{
			Transport: &heroku.Transport{
				Username: os.Getenv(HerokuLogin),
				Password: os.Getenv(HerokuToken),
			},
		},
	)

	g := github.NewClient(
		oauth2.NewClient(
			context.Background(),
			oauth2.StaticTokenSource(
				&oauth2.Token{AccessToken: os.Getenv(GitHubToken)},
			),
		),
	)

	r := mux.NewRouter()
	r.Handle("/webhook", handler(g, h, []byte(secret), app, org)).Methods(http.MethodPost)

	RunServer(port, 10*time.Second, r)
}

func handler(g *github.Client, h *heroku.Service, secret []byte, app, org string) http.Handler {
	t := &tokenCache{}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		payload, err := github.ValidatePayload(r, secret)
		if err != nil {
			log.Printf("failed to validate webhook: %v", err)
			http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)

			return
		}

		if github.WebHookType(r) != "workflow_job" {
			log.Printf("unexpected webhook type: %v", github.WebHookType(r))
			w.WriteHeader(http.StatusAccepted)

			return
		}

		event := &WorkflowJobEvent{}
		if err := json.Unmarshal(payload, &event); err != nil {
			log.Printf("failed to parse webhook: %v", err)
			http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)

			return
		}

		if event.Action != "queued" {
			log.Printf("action is not queued, not creating a new dyno. (%v)", event.Action)

			return
		}

		token, err := t.Get(r.Context(), g, org)
		if err != nil {
			log.Printf("error creating registration token: %v", err)
			w.WriteHeader(http.StatusInternalServerError)

			return
		}

		d, err := h.DynoCreate(r.Context(), app, heroku.DynoCreateOpts{
			Attach:  heroku.Bool(false),
			Command: fmt.Sprintf("./config.sh --unattended --ephemeral --url https://github.com/%v --token %v && ./run.sh", org, token),
		})
		if err != nil {
			log.Printf("error creating dyno: %v", err)
			w.WriteHeader(http.StatusInternalServerError)

			return
		}

		log.Printf("payload: %+v", event.WorkflowJob)
		log.Printf("dyno: %+v", d)
	})
}

func logginHandler(n http.Handler) http.Handler {
	return handlers.LoggingHandler(os.Stdout, n)
}

func timeoutHandler(t time.Duration) func(http.Handler) http.Handler {
	return func(n http.Handler) http.Handler {
		return http.TimeoutHandler(n, t, http.StatusText(http.StatusServiceUnavailable))
	}
}

func RunServer(port string, timeout time.Duration, r *mux.Router) error {
	r.Use(
		logginHandler,
		timeoutHandler(timeout),
	)

	r.PathPrefix("/").HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)
		},
	)

	server := &http.Server{
		Addr:         "0.0.0.0:" + port,
		WriteTimeout: time.Second * 15,
		ReadTimeout:  time.Second * 15,
		IdleTimeout:  time.Second * 60,
		Handler:      r,
	}

	errorC := make(chan error, 1)
	shutdownC := make(chan os.Signal, 1)

	go func(errC chan<- error) {
		errC <- server.ListenAndServe()
	}(errorC)

	signal.Notify(shutdownC, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-errorC:
		if err != nil && err != http.ErrServerClosed {
			return err
		}

		return nil
	case <-shutdownC:
		return shutdown(server)
	}
}

func shutdown(server *http.Server) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	return server.Shutdown(ctx)
}
