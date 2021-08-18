package app

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/aws/aws-lambda-go/lambdacontext"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/lambda"
	"github.com/caarlos0/env"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/transport/http"
)

// Config holds all variables read from the ENV
type Config struct {
	Region   string `env:"REGION" envDefault:"us-east-1"`
	Bucket   string `env:"BUCKET"`
	RepoURL  string `env:"REPO_URL"`
	GitToken string `env:"GIT_TOKEN" envDefault:""`
}

// Request is a struct that contains the request data.
type Request struct {
	ID        string                 `json:"id"`
	Name      string                 `json:"name"`
	Version   string                 `json:"version"`
	Variables map[string]interface{} `json:"variables"`
}

type App struct {
	sess   *session.Session
	bucket string
	repo   string
	token  string
	cpus   int
}

func New() (*App, error) {
	cfg := Config{}

	err := env.Parse(&cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to parse ENV: %w", err)
	}

	sess, err := session.NewSession(&aws.Config{Region: aws.String(cfg.Region)})
	if err != nil {
		return nil, fmt.Errorf("failed to create AWS session: %w", err)
	}

	return &App{
		cpus:   runtime.NumCPU(),
		bucket: cfg.Bucket,
		token:  cfg.GitToken,
		repo:   cfg.RepoURL,
		sess:   sess,
	}, nil
}

func (a *App) Run(ctx context.Context, raw []byte) error {
	var requests []*Request
	err := json.Unmarshal(raw, &requests)
	if err != nil {
		return fmt.Errorf("failed to unmarshal requests: %w", err)
	}

	if len(requests) > a.cpus {
		// Execute no more than the number of CPUs
		// Send the rest to a new Lambda invocation
		return a.dispatch(requests[a.cpus:])
	}

	for _, req := range requests[:a.cpus] {
		err := a.execute(req)
		if err != nil {
			return err
		}
	}

	return nil
}

// dispatch passes all requests beyond the number of CPUs to a new Lambda invocation
func (a *App) dispatch(requests []*Request) error {
	b, err := json.Marshal(requests)
	if err != nil {
		return fmt.Errorf("error marshalling requests: %w", err)
	}

	l := lambda.New(a.sess)
	_, err = l.Invoke(&lambda.InvokeInput{
		FunctionName: aws.String(lambdacontext.FunctionName),
		Payload:      b,
	})
	if err != nil {
		return fmt.Errorf("failed to dispatch requests: %w", err)
	}

	return nil
}

// execute runs terraform using the given request
func (a *App) execute(req *Request) error {
	path := filepath.Join("/tmp", req.Name)

	err := a.checkout(path, req.Version)
	if err != nil {
		return err
	}

	err = a.createBackend(path, req.Name)
	if err != nil {
		return err
	}

	buf := newWrappedWriter(req.Name)

	cmd := exec.Command("terraform", "apply", "-input=false", "-auto-approve")
	cmd.Stderr = buf
	cmd.Stdout = buf
	cmd.Env = getEnv(req.Variables)

	err = cmd.Run()
	if err != nil {
		return fmt.Errorf("failed to run terraform: %w", err)
	}

	return nil
}

func (a *App) checkout(path string, version string) error {
	var auth http.BasicAuth
	if len(a.token) > 0 {
		auth = http.BasicAuth{Username: "git", Password: a.token}
	}

	repo, err := git.PlainClone(path, false, &git.CloneOptions{
		URL:  a.repo,
		Tags: git.AllTags,
		Auth: &auth,
	})
	if err != nil {
		return fmt.Errorf("failed clone repository: %w", err)
	}

	tree, err := repo.Worktree()
	if err != nil {
		return fmt.Errorf("failed to get worktree: %w", err)
	}

	err = tree.Checkout(&git.CheckoutOptions{
		Branch: plumbing.ReferenceName(fmt.Sprintf("refs/tags/%s", version)),
	})
	if err != nil {
		return fmt.Errorf("failed to checkout branch: %w", err)
	}

	return nil
}

func (a *App) createBackend(path, name string) error {
	p := filepath.Join(path, "backend.tf")
	err := os.WriteFile(p, []byte(fmt.Sprintf(`terraform {
		backend "s3" {
			bucket = "%s"
			key    = "%s.tfstate"
			region = "%s"
		}
}`, a.bucket, name, aws.StringValue(a.sess.Config.Region))), 0600)
	if err != nil {
		return fmt.Errorf("failed to write %s: %w", p, err)
	}

	return nil
}

func getEnv(m map[string]interface{}) (vars []string) {
	for k, val := range m {
		switch v := val.(type) {
		case bool:
			vars = append(vars, fmt.Sprintf("%s=%t", k, v))
		case string:
			vars = append(vars, fmt.Sprintf("%s=%s", k, v))
		case int64:
			vars = append(vars, fmt.Sprintf("%s=%d", k, v))
		case float64:
			vars = append(vars, fmt.Sprintf("%s=%f", k, v))
		case map[string]interface{}:
			b, err := json.Marshal(v)
			if err != nil {
				vars = append(vars, fmt.Sprintf("%s=%v", k, v))

				break
			}
			vars = append(vars, fmt.Sprintf("%s=%s", k, string(b)))
		}
	}

	return
}

type wrappedWriter struct {
	ident string
}

func newWrappedWriter(ident string) *wrappedWriter {
	return &wrappedWriter{ident: ident}
}

func (w *wrappedWriter) Write(b []byte) (int, error) {
	now := time.Now()
	scan := bufio.NewScanner(bytes.NewReader(b))
	written := 0

	for scan.Scan() {
		n, err := fmt.Fprintf(os.Stdout, "[%s][%s]: %s\n", now.Format("15:04:05"), w.ident, scan.Text())
		if err != nil {
			return written, fmt.Errorf("failed to write to stdout: %w", err)
		}
		written += n
	}
	err := scan.Err()
	if err != nil {
		return written, fmt.Errorf("failed when scanning for a new line: %w", err)
	}

	return written, nil
}
