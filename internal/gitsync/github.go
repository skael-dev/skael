package gitsync

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ErrNotFound is GitHub's 404: a missing repo, branch or installation, or one
// the App cannot see.
var ErrNotFound = errors.New("not found on GitHub, or the App cannot see it")

// APIError is a GitHub answer other than success or 404.
type APIError struct {
	Status int
	Body   string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("GitHub returned %d: %s", e.Status, e.Body)
}

// GitHub calls the GitHub API as the App and as its installations.
type GitHub struct {
	app    *App
	key    *rsa.PrivateKey
	client *http.Client

	mu     sync.Mutex
	tokens map[int64]installationToken
}

type installationToken struct {
	token   string
	expires time.Time
}

// NewGitHub builds a client for app.
func NewGitHub(app *App, client *http.Client) (*GitHub, error) {
	block, _ := pem.Decode(app.PrivateKey)
	if block == nil {
		return nil, errors.New("gitsync: the App's private key is not PEM")
	}
	var key *rsa.PrivateKey
	if k, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		key = k
	} else {
		parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("gitsync: parse the App's private key: %w", err)
		}
		rsaKey, ok := parsed.(*rsa.PrivateKey)
		if !ok {
			return nil, errors.New("gitsync: the App's private key is not RSA")
		}
		key = rsaKey
	}
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	return &GitHub{app: app, key: key, client: client, tokens: map[int64]installationToken{}}, nil
}

// appJWT signs the short-lived token that authenticates the App itself.
func (g *GitHub) appJWT(now time.Time) (string, error) {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	claims, err := json.Marshal(map[string]any{
		// Backdated a minute for clock drift; GitHub refuses more than ten.
		"iat": now.Add(-time.Minute).Unix(),
		"exp": now.Add(9 * time.Minute).Unix(),
		"iss": strconv.FormatInt(g.app.AppID, 10),
	})
	if err != nil {
		return "", err
	}
	signing := header + "." + base64.RawURLEncoding.EncodeToString(claims)
	sum := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, g.key, crypto.SHA256, sum[:])
	if err != nil {
		return "", fmt.Errorf("gitsync: sign the App token: %w", err)
	}
	return signing + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

// call sends one request with a bearer token and decodes a JSON answer into out.
func (g *GitHub) call(ctx context.Context, method, path, token string, body, out any) error {
	var rd io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimSuffix(g.app.APIBaseURL, "/")+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := g.client.Do(req)
	if err != nil {
		return fmt.Errorf("gitsync: %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode == http.StatusNotFound {
		return ErrNotFound
	}
	if resp.StatusCode >= 300 {
		return &APIError{Status: resp.StatusCode, Body: strings.TrimSpace(string(raw))}
	}
	if out == nil || len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, out)
}

// asApp calls as the App itself.
func (g *GitHub) asApp(ctx context.Context, method, path string, body, out any) error {
	jwt, err := g.appJWT(time.Now())
	if err != nil {
		return err
	}
	return g.call(ctx, method, path, jwt, body, out)
}

// Token returns an installation token, cached until five minutes before it
// expires.
func (g *GitHub) Token(ctx context.Context, installationID int64) (string, error) {
	g.mu.Lock()
	t, ok := g.tokens[installationID]
	g.mu.Unlock()
	if ok && time.Until(t.expires) > 5*time.Minute {
		return t.token, nil
	}
	var out struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := g.asApp(ctx, http.MethodPost, fmt.Sprintf("/app/installations/%d/access_tokens", installationID), nil, &out); err != nil {
		return "", err
	}
	g.mu.Lock()
	g.tokens[installationID] = installationToken{token: out.Token, expires: out.ExpiresAt}
	g.mu.Unlock()
	return out.Token, nil
}

// asInstallation calls as one installation.
func (g *GitHub) asInstallation(ctx context.Context, installationID int64, method, path string, body, out any) error {
	token, err := g.Token(ctx, installationID)
	if err != nil {
		return err
	}
	return g.call(ctx, method, path, token, body, out)
}

// Repo is a repository an installation can reach.
type Repo struct {
	InstallationID int64  `json:"installation_id"`
	Owner          string `json:"owner"`
	Name           string `json:"name"`
	DefaultBranch  string `json:"default_branch"`
	Private        bool   `json:"private"`
}

// Repos lists every repository the App's installations can reach.
func (g *GitHub) Repos(ctx context.Context) ([]Repo, error) {
	var installs []struct {
		ID int64 `json:"id"`
	}
	if err := g.asApp(ctx, http.MethodGet, "/app/installations?per_page=100", nil, &installs); err != nil {
		return nil, err
	}
	var out []Repo
	for _, in := range installs {
		for page := 1; ; page++ {
			var resp struct {
				Repositories []struct {
					Name          string `json:"name"`
					DefaultBranch string `json:"default_branch"`
					Private       bool   `json:"private"`
					Owner         struct {
						Login string `json:"login"`
					} `json:"owner"`
				} `json:"repositories"`
			}
			if err := g.asInstallation(ctx, in.ID, http.MethodGet,
				fmt.Sprintf("/installation/repositories?per_page=100&page=%d", page), nil, &resp); err != nil {
				return nil, err
			}
			for _, r := range resp.Repositories {
				out = append(out, Repo{InstallationID: in.ID, Owner: r.Owner.Login, Name: r.Name,
					DefaultBranch: r.DefaultBranch, Private: r.Private})
			}
			if len(resp.Repositories) < 100 {
				break
			}
		}
	}
	return out, nil
}

// RepoInstallation returns the installation that reaches owner/repo.
func (g *GitHub) RepoInstallation(ctx context.Context, owner, repo string) (int64, error) {
	var out struct {
		ID int64 `json:"id"`
	}
	if err := g.asApp(ctx, http.MethodGet, "/repos/"+url.PathEscape(owner)+"/"+url.PathEscape(repo)+"/installation", nil, &out); err != nil {
		return 0, err
	}
	return out.ID, nil
}

// BranchHead returns the commit a branch points at.
func (g *GitHub) BranchHead(ctx context.Context, installationID int64, owner, repo, branch string) (string, error) {
	var out struct {
		Commit struct {
			SHA string `json:"sha"`
		} `json:"commit"`
	}
	path := "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repo) + "/branches/" + url.PathEscape(branch)
	if err := g.asInstallation(ctx, installationID, http.MethodGet, path, nil, &out); err != nil {
		return "", err
	}
	return out.Commit.SHA, nil
}

// CommitAuthor returns the GitHub login of a commit's author, or "" when the
// commit's email matches no account.
func (g *GitHub) CommitAuthor(ctx context.Context, installationID int64, owner, repo, sha string) (string, error) {
	var out struct {
		Author *struct {
			Login string `json:"login"`
		} `json:"author"`
	}
	path := "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repo) + "/commits/" + url.PathEscape(sha)
	if err := g.asInstallation(ctx, installationID, http.MethodGet, path, nil, &out); err != nil {
		return "", err
	}
	if out.Author == nil {
		return "", nil
	}
	return out.Author.Login, nil
}

// FolderFile is one file of a skill folder to commit.
type FolderFile struct {
	Path       string // relative to the folder
	Content    []byte
	Executable bool
}

type treeEntry struct {
	Path string  `json:"path"`
	Mode string  `json:"mode"`
	Type string  `json:"type"`
	SHA  *string `json:"sha"`
}

// CommitFolder commits files as the whole content of dir on a new branch cut
// from base, and returns the new commit. A file under dir that files lacks is
// deleted, so the folder in git matches the archive exactly.
func (g *GitHub) CommitFolder(ctx context.Context, installationID int64, owner, repo, base, branch, dir string,
	files []FolderFile, message string) (string, error) {
	prefix := "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repo)
	baseSHA, err := g.BranchHead(ctx, installationID, owner, repo, base)
	if err != nil {
		return "", err
	}
	var commit struct {
		Tree struct {
			SHA string `json:"sha"`
		} `json:"tree"`
	}
	if err := g.asInstallation(ctx, installationID, http.MethodGet, prefix+"/git/commits/"+baseSHA, nil, &commit); err != nil {
		return "", err
	}
	var tree struct {
		Tree []struct {
			Path string `json:"path"`
			Type string `json:"type"`
		} `json:"tree"`
	}
	if err := g.asInstallation(ctx, installationID, http.MethodGet, prefix+"/git/trees/"+commit.Tree.SHA+"?recursive=1", nil, &tree); err != nil {
		return "", err
	}

	dir = strings.Trim(dir, "/")
	written := map[string]bool{}
	var entries []treeEntry
	for _, f := range files {
		var blob struct {
			SHA string `json:"sha"`
		}
		if err := g.asInstallation(ctx, installationID, http.MethodPost, prefix+"/git/blobs", map[string]string{
			"content": base64.StdEncoding.EncodeToString(f.Content), "encoding": "base64",
		}, &blob); err != nil {
			return "", err
		}
		mode := "100644"
		if f.Executable {
			mode = "100755"
		}
		p := dir + "/" + strings.TrimPrefix(f.Path, "/")
		sha := blob.SHA
		entries = append(entries, treeEntry{Path: p, Mode: mode, Type: "blob", SHA: &sha})
		written[p] = true
	}
	for _, e := range tree.Tree {
		if e.Type == "blob" && strings.HasPrefix(e.Path, dir+"/") && !written[e.Path] {
			entries = append(entries, treeEntry{Path: e.Path, Mode: "100644", Type: "blob", SHA: nil})
		}
	}

	var newTree, newCommit struct {
		SHA string `json:"sha"`
	}
	if err := g.asInstallation(ctx, installationID, http.MethodPost, prefix+"/git/trees", map[string]any{
		"base_tree": commit.Tree.SHA, "tree": entries,
	}, &newTree); err != nil {
		return "", err
	}
	if err := g.asInstallation(ctx, installationID, http.MethodPost, prefix+"/git/commits", map[string]any{
		"message": message, "tree": newTree.SHA, "parents": []string{baseSHA},
	}, &newCommit); err != nil {
		return "", err
	}
	if err := g.asInstallation(ctx, installationID, http.MethodPost, prefix+"/git/refs", map[string]string{
		"ref": "refs/heads/" + branch, "sha": newCommit.SHA,
	}, nil); err != nil {
		return "", err
	}
	return newCommit.SHA, nil
}

// PullRequest is the part of a pull request git sync reads.
type PullRequest struct {
	Number         int    `json:"number"`
	HTMLURL        string `json:"html_url"`
	State          string `json:"state"`
	Merged         bool   `json:"merged"`
	MergeCommitSHA string `json:"merge_commit_sha"`
}

// OpenPullRequest opens a pull request from head into base.
func (g *GitHub) OpenPullRequest(ctx context.Context, installationID int64, owner, repo, head, base, title, body string) (*PullRequest, error) {
	var pr PullRequest
	err := g.asInstallation(ctx, installationID, http.MethodPost,
		"/repos/"+url.PathEscape(owner)+"/"+url.PathEscape(repo)+"/pulls", map[string]string{
			"title": title, "head": head, "base": base, "body": body,
		}, &pr)
	return &pr, err
}

// GetPullRequest reads one pull request.
func (g *GitHub) GetPullRequest(ctx context.Context, installationID int64, owner, repo string, number int) (*PullRequest, error) {
	var pr PullRequest
	err := g.asInstallation(ctx, installationID, http.MethodGet,
		fmt.Sprintf("/repos/%s/%s/pulls/%d", url.PathEscape(owner), url.PathEscape(repo), number), nil, &pr)
	return &pr, err
}

// DeleteBranch removes a branch Skael created. A branch GitHub already
// deleted is not an error.
func (g *GitHub) DeleteBranch(ctx context.Context, installationID int64, owner, repo, branch string) error {
	err := g.asInstallation(ctx, installationID, http.MethodDelete,
		"/repos/"+url.PathEscape(owner)+"/"+url.PathEscape(repo)+"/git/refs/heads/"+branch, nil, nil)
	var apiErr *APIError
	if errors.Is(err, ErrNotFound) || (errors.As(err, &apiErr) && apiErr.Status == http.StatusUnprocessableEntity) {
		return nil
	}
	return err
}

// OpenPull is an open pull request into a connection's branch.
type OpenPull struct {
	Number  int    `json:"number"`
	HTMLURL string `json:"html_url"`
	Title   string `json:"title"`
	Draft   bool   `json:"draft"`
	Labels  []struct {
		Name string `json:"name"`
	} `json:"labels"`
	Head struct {
		SHA  string `json:"sha"`
		Repo *struct {
			FullName string `json:"full_name"`
		} `json:"repo"`
	} `json:"head"`
	Base struct {
		Repo struct {
			FullName string `json:"full_name"`
		} `json:"repo"`
	} `json:"base"`
}

// Fork reports whether the pull request comes from another repository.
func (p *OpenPull) Fork() bool {
	return p.Head.Repo == nil || !strings.EqualFold(p.Head.Repo.FullName, p.Base.Repo.FullName)
}

// HasLabel reports whether the pull request carries label.
func (p *OpenPull) HasLabel(label string) bool {
	for _, l := range p.Labels {
		if strings.EqualFold(l.Name, label) {
			return true
		}
	}
	return false
}

// OpenPulls lists the open pull requests into base.
func (g *GitHub) OpenPulls(ctx context.Context, installationID int64, owner, repo, base string) ([]OpenPull, error) {
	var out []OpenPull
	for page := 1; ; page++ {
		var batch []OpenPull
		path := fmt.Sprintf("/repos/%s/%s/pulls?state=open&base=%s&per_page=100&page=%d",
			url.PathEscape(owner), url.PathEscape(repo), url.QueryEscape(base), page)
		if err := g.asInstallation(ctx, installationID, http.MethodGet, path, nil, &batch); err != nil {
			return nil, err
		}
		out = append(out, batch...)
		if len(batch) < 100 {
			return out, nil
		}
	}
}

// PullFiles lists the paths a pull request changes, including the old path
// of a rename.
func (g *GitHub) PullFiles(ctx context.Context, installationID int64, owner, repo string, number int) ([]string, error) {
	var out []string
	for page := 1; ; page++ {
		var batch []struct {
			Filename         string `json:"filename"`
			PreviousFilename string `json:"previous_filename"`
		}
		path := fmt.Sprintf("/repos/%s/%s/pulls/%d/files?per_page=100&page=%d",
			url.PathEscape(owner), url.PathEscape(repo), number, page)
		if err := g.asInstallation(ctx, installationID, http.MethodGet, path, nil, &batch); err != nil {
			return nil, err
		}
		for _, f := range batch {
			out = append(out, f.Filename)
			if f.PreviousFilename != "" {
				out = append(out, f.PreviousFilename)
			}
		}
		if len(batch) < 100 {
			return out, nil
		}
	}
}

// CheckRun is the part of a check run git sync writes.
type CheckRun struct {
	Name       string           `json:"name,omitempty"`
	HeadSHA    string           `json:"head_sha,omitempty"`
	Status     string           `json:"status,omitempty"`
	Conclusion string           `json:"conclusion,omitempty"`
	Output     *CheckRunOutput  `json:"output,omitempty"`
	Actions    []CheckRunAction `json:"actions,omitempty"`
}

// CheckRunOutput is a check run's title and markdown summary.
type CheckRunOutput struct {
	Title   string `json:"title"`
	Summary string `json:"summary"`
}

// CheckRunAction is a button on a check run. GitHub sends a requested_action
// event with its identifier when someone clicks it.
type CheckRunAction struct {
	Label       string `json:"label"`
	Description string `json:"description"`
	Identifier  string `json:"identifier"`
}

// CreateCheckRun posts a completed check run and returns its id.
func (g *GitHub) CreateCheckRun(ctx context.Context, installationID int64, owner, repo string, run CheckRun) (int64, error) {
	var out struct {
		ID int64 `json:"id"`
	}
	err := g.asInstallation(ctx, installationID, http.MethodPost,
		"/repos/"+url.PathEscape(owner)+"/"+url.PathEscape(repo)+"/check-runs", run, &out)
	return out.ID, err
}

// UpdateCheckRun rewrites a check run.
func (g *GitHub) UpdateCheckRun(ctx context.Context, installationID int64, owner, repo string, id int64, run CheckRun) error {
	return g.asInstallation(ctx, installationID, http.MethodPatch,
		fmt.Sprintf("/repos/%s/%s/check-runs/%d", url.PathEscape(owner), url.PathEscape(repo), id), run, nil)
}
