package gitsync

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/rs/zerolog/log"
)

// WebhookPath is where GitHub delivers the App's events. The auth middleware
// passes it: the signature is the authentication.
const WebhookPath = "/api/github/webhook"

const maxWebhookBody = 25 << 20

// verifySignature checks GitHub's X-Hub-Signature-256 over the raw body.
func verifySignature(secret, body []byte, header string) bool {
	sig, ok := strings.CutPrefix(header, "sha256=")
	if !ok {
		return false
	}
	want, err := hex.DecodeString(sig)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	return hmac.Equal(mac.Sum(nil), want)
}

// WebhookHandler receives GitHub's events. A push only brings a sync forward:
// the sync reads the branch itself, so a forged or replayed event can at most
// cause a sync that changes nothing.
func WebhookHandler(store *Store, jobs JobInserter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if jobs == nil {
			http.Error(w, "background jobs are not running", http.StatusServiceUnavailable)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, maxWebhookBody))
		if err != nil {
			http.Error(w, "read body", http.StatusBadRequest)
			return
		}
		app, err := store.App(r.Context())
		if err != nil {
			log.Error().Err(err).Msg("gitsync: webhook: load the App")
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if app == nil {
			http.Error(w, "no GitHub App is connected", http.StatusNotFound)
			return
		}
		if !verifySignature(app.WebhookSecret, body, r.Header.Get("X-Hub-Signature-256")) {
			http.Error(w, "signature does not match", http.StatusUnauthorized)
			return
		}

		switch r.Header.Get("X-GitHub-Event") {
		case "pull_request":
			// A sync reads every open change's pull request, so any pull
			// request event on a connected repo only brings that forward.
			var p struct {
				Repository struct {
					Name  string `json:"name"`
					Owner struct {
						Login string `json:"login"`
					} `json:"owner"`
				} `json:"repository"`
			}
			if err := json.Unmarshal(body, &p); err != nil {
				http.Error(w, "decode pull request", http.StatusBadRequest)
				return
			}
			conns, err := store.ConnectionsForRepo(r.Context(), p.Repository.Owner.Login, p.Repository.Name)
			if err != nil {
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			for _, c := range conns {
				_ = store.TouchWebhook(r.Context(), c.ID)
				if _, err := jobs.Insert(r.Context(), SyncArgs{ConnectionID: c.ID}, nil); err != nil {
					log.Error().Err(err).Str("connection", c.ID).Msg("gitsync: webhook: enqueue sync")
					http.Error(w, "internal error", http.StatusInternalServerError)
					return
				}
			}
		case "check_run":
			// The check run's "Run contest" button.
			var p struct {
				Action          string `json:"action"`
				RequestedAction struct {
					Identifier string `json:"identifier"`
				} `json:"requested_action"`
				CheckRun struct {
					HeadSHA      string `json:"head_sha"`
					PullRequests []struct {
						Number int `json:"number"`
					} `json:"pull_requests"`
				} `json:"check_run"`
				Repository struct {
					Name  string `json:"name"`
					Owner struct {
						Login string `json:"login"`
					} `json:"owner"`
				} `json:"repository"`
			}
			if err := json.Unmarshal(body, &p); err != nil {
				http.Error(w, "decode check run", http.StatusBadRequest)
				return
			}
			if p.Action != "requested_action" || p.RequestedAction.Identifier != "contest" {
				break
			}
			conns, err := store.ConnectionsForRepo(r.Context(), p.Repository.Owner.Login, p.Repository.Name)
			if err != nil {
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			for _, c := range conns {
				requested := false
				for _, pr := range p.CheckRun.PullRequests {
					ok, err := store.RequestContest(r.Context(), c.ID, pr.Number, p.CheckRun.HeadSHA)
					if err != nil {
						http.Error(w, "internal error", http.StatusInternalServerError)
						return
					}
					requested = requested || ok
				}
				if requested {
					_ = store.TouchWebhook(r.Context(), c.ID)
					if _, err := jobs.Insert(r.Context(), SyncArgs{ConnectionID: c.ID}, nil); err != nil {
						http.Error(w, "internal error", http.StatusInternalServerError)
						return
					}
				}
			}
		case "push":
			var p struct {
				Ref        string `json:"ref"`
				Repository struct {
					Name  string `json:"name"`
					Owner struct {
						Login string `json:"login"`
						Name  string `json:"name"`
					} `json:"owner"`
				} `json:"repository"`
			}
			if err := json.Unmarshal(body, &p); err != nil {
				http.Error(w, "decode push", http.StatusBadRequest)
				return
			}
			owner := p.Repository.Owner.Login
			if owner == "" {
				owner = p.Repository.Owner.Name
			}
			branch, ok := strings.CutPrefix(p.Ref, "refs/heads/")
			if !ok {
				w.WriteHeader(http.StatusAccepted)
				return
			}
			conns, err := store.ConnectionsForRepo(r.Context(), owner, p.Repository.Name)
			if err != nil {
				log.Error().Err(err).Msg("gitsync: webhook: connections")
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			for _, c := range conns {
				if err := store.TouchWebhook(r.Context(), c.ID); err != nil {
					log.Warn().Err(err).Str("connection", c.ID).Msg("gitsync: webhook: touch")
				}
				if c.Branch != branch {
					continue
				}
				if _, err := jobs.Insert(r.Context(), SyncArgs{ConnectionID: c.ID}, nil); err != nil {
					log.Error().Err(err).Str("connection", c.ID).Msg("gitsync: webhook: enqueue sync")
					http.Error(w, "internal error", http.StatusInternalServerError)
					return
				}
			}
		}
		w.WriteHeader(http.StatusAccepted)
	})
}
