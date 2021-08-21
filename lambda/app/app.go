package app

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"

	"github.com/aws/aws-lambda-go/lambdacontext"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/lambda"
	"github.com/aws/aws-sdk-go/service/sts"
	"github.com/caarlos0/env"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	ghttp "github.com/go-git/go-git/v5/plumbing/transport/http"
)

// Config holds all variables read from the ENV
type Config struct {
	Region   string `env:"REGION" envDefault:"us-east-1"`
	Bucket   string `env:"BUCKET"`
	RepoURL  string `env:"REPO_URL"`
	GitToken string `env:"GIT_TOKEN" envDefault:""`
	RoleName string `env:"ROLE_NAME" envDefault:"OrganizationAccountAccessRole"`
}

// Request is a struct that contains the request data.
type Request struct {
	ID        string                 `json:"id"`
	Name      string                 `json:"name"`
	Version   string                 `json:"version"`
	LogLevel  string                 `json:"log_level"`
	Variables map[string]interface{} `json:"variables"`
}

type App struct {
	sess   *session.Session
	bucket string
	repo   string
	token  string
	role   string
	tf     string
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
		role:   cfg.RoleName,
		sess:   sess,
	}, nil
}

func (a *App) Run(ctx context.Context, requests []*Request) error {
	end := len(requests)
	if end > a.cpus {
		// Execute no more than the number of CPUs
		// Send the rest to a new Lambda invocation
		end = a.cpus
		err := a.dispatch(requests[a.cpus:])
		if err != nil {
			return err
		}
	}

	err := a.prepTf()
	if err != nil {
		return err
	}

	var wg sync.WaitGroup
	wg.Add(end)
	for _, req := range requests[:end] {
		req := req
		go func(req *Request, wg *sync.WaitGroup) {
			err := a.execute(req)
			if err != nil {
				log.Printf("failed to execute: %s -> %v\n", req.Name, err)
			}
			wg.Done()
		}(req, &wg)
	}

	wg.Wait()

	return nil
}

// dispatch passes all requests beyond the number of CPUs to a new Lambda invocation
func (a *App) dispatch(requests []*Request) error {
	b, err := json.Marshal(requests)
	if err != nil {
		return fmt.Errorf("error marshaling requests: %w", err)
	}

	l := lambda.New(a.sess)
	_, err = l.Invoke(&lambda.InvokeInput{
		FunctionName:   aws.String(lambdacontext.FunctionName),
		InvocationType: aws.String(lambda.InvocationTypeEvent),
		Payload:        b,
	})
	if err != nil {
		return fmt.Errorf("failed to dispatch requests: %w", err)
	}

	return nil
}

func (a *App) prepTf() error {
	var err error
	a.tf, err = getTerraform("latest")
	if err != nil {
		return err
	}

	err = os.Chmod(a.tf, 0750) // #nosec
	if err != nil {
		return fmt.Errorf("failed to make terraform executable: %w", err)
	}

	return nil
}

// execute runs terraform using the given request
func (a *App) execute(req *Request) error {
	path := filepath.Join("/tmp", req.Name)

	err := a.checkout(a.repo, path, req.Version)
	if err != nil {
		return err
	}

	creds, err := a.sess.Config.Credentials.Get()
	if err != nil {
		return fmt.Errorf("failed to get AWS credentials: %w", err)
	}

	err = a.createBackend(creds, path, req.Name)
	if err != nil {
		return err
	}

	roleArn := fmt.Sprintf("arn:aws:iam::%s:role/%s", req.ID, a.role)
	cred, err := a.assumeRole(req.Name, roleArn)
	if err != nil {
		return err
	}

	pluginDir := filepath.Join("/tmp", "terraform.d", "plugins")
	err = os.MkdirAll(pluginDir, 0750)
	if err != nil {
		return fmt.Errorf("failed to create plugins directory: %w", err)
	}

	modDir := filepath.Join(path, ".terraform", "modules")
	err = os.MkdirAll(modDir, 0750)
	if err != nil {
		return fmt.Errorf("failed to create .terraform/modules directory: %w", err)
	}

	err = a.getModules(filepath.Join(path, "main.tf"))
	if err != nil {
		return err
	}

	env := getEnv(req.Variables)

	// Add assumed role credential to the env for this process
	env = append(env, getCredEnv(cred)...)
	env = append(env, fmt.Sprintf("TF_PLUGIN_CACHE_DIR=%s", pluginDir))
	env = append(env, fmt.Sprintf("TF_LOG=%s", req.LogLevel))
	env = append(env, fmt.Sprintf("HOME=%s", path))

	err = a.createGitConfig(filepath.Join(path, ".git", "config"))
	if err != nil {
		return err
	}

	init, err := a.runTf(path, req, env, "init", "-input=false", "-no-color")
	if err != nil {
		return err
	}
	err = init.Wait()
	if err != nil {
		return err
	}

	apply, err := a.runTf(path, req, env, "apply", "-input=false", "-auto-approve", "-no-color")
	if err != nil {
		return err
	}

	return apply.Wait()
}

func (a *App) checkout(repoURL string, path string, version string) error {
	var auth ghttp.BasicAuth
	if len(a.token) > 0 {
		auth = ghttp.BasicAuth{Username: "git", Password: a.token}
	}

	err := os.RemoveAll(path)
	if err != nil {
		log.Printf("failed to remove old repository directory: %s -> %v", path, err)
	}

	repo, err := git.PlainClone(path, false, &git.CloneOptions{
		URL:  repoURL,
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

func (a *App) getModules(path string) error {
	modules, err := readModules(path)
	if err != nil {
		return err
	}

	modpath := filepath.Join(filepath.Dir(path), ".terraform", "modules")

	for _, m := range modules {
		u, ref, err := normalizedSource(m.Source)
		if err != nil {
			return err
		}
		err = a.checkout(u, filepath.Join(modpath, m.Key), ref)
		if err != nil {
			return err
		}
	}
	d := struct {
		Modules []Module
	}{modules}

	f, err := os.Create(filepath.Join(modpath, "modules.json"))
	if err != nil {
		return fmt.Errorf("failed to create modules.json: %w", err)
	}
	err = json.NewEncoder(f).Encode(d)
	if err != nil {
		return fmt.Errorf("failed to encode modules.json: %w", err)
	}

	return nil
}

func normalizedSource(source string) (string, string, error) {
	u, err := url.Parse(source)
	if err != nil {
		return "", "", fmt.Errorf("failed to parse source URL: %w", err)
	}

	u.Scheme = "https"

	ref := ""
	if v, ok := u.Query()["ref"]; ok {
		ref = v[0]
	}

	// drop anything after a .git
	n := strings.Index(u.Path, ".git")
	if n > -1 {
		u.Path = u.Path[:n]
	}

	u.RawQuery = ""

	return u.String(), ref, nil
}

type Module struct {
	Key     string
	Source  string
	Dir     string
	RootDir string `json:"-"`
}

func readModules(path string) ([]Module, error) {
	r := regexp.MustCompile(`module "(.*)" {\s+source\s+=\s+"(.*)"`)

	content, err := ioutil.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil, fmt.Errorf("failed to read file %s -> %w", path, err)
	}

	matches := r.FindAllStringSubmatch(string(content), -1)
	if len(matches) == 0 {
		return nil, fmt.Errorf("failed to find a module in %s", path)
	}

	var modules []Module
	for _, match := range matches {
		dir := fmt.Sprintf(".terraform/modules/%s", match[1])
		rootDir := dir

		source := match[2]
		u, err := url.Parse(source)
		if err != nil {
			return nil, fmt.Errorf("failed to parse URL for module source %s -> %w", source, err)
		}

		n := strings.Index(u.Path, ".git")
		if n > -1 {
			// grab anything after .git, split it and join it back together to remove any duplicate slashes
			// append it to the dir to ensure terraform respects the subdir
			dir = filepath.Join(dir, filepath.Join(strings.Split(u.Path[n+4:], "/")...))
		}

		modules = append(modules, Module{
			Key:     match[1],
			Source:  match[2],
			Dir:     dir,
			RootDir: rootDir,
		})
	}

	return modules, nil
}

func (a *App) createGitConfig(path string) error {
	content := fmt.Sprintf("[url \"https://%s@github.com\"]\n\tinsteadOf = https://github.com\n", a.token)

	err := os.WriteFile(path, []byte(content), 0600)
	if err != nil {
		return fmt.Errorf("failed to write %s -> %w", path, err)
	}

	return nil
}

func (a *App) createBackend(creds credentials.Value, path, name string) error {
	p := filepath.Join(path, "backend.tf")
	err := os.WriteFile(p, []byte(fmt.Sprintf(`terraform {
			backend "s3" {
				bucket     = "%s"
				key        = "%s.tfstate"
				region     = "%s"
				access_key = "%s"
				secret_key = "%s"
				token      = "%s"
			}
	}`, a.bucket, name, aws.StringValue(a.sess.Config.Region),
		creds.AccessKeyID, creds.SecretAccessKey, creds.SessionToken)), 0600)
	if err != nil {
		return fmt.Errorf("failed to write %s: %w", p, err)
	}

	return nil
}

func (a *App) assumeRole(name, role string) (*sts.Credentials, error) {
	svc := sts.New(a.sess)

	resp, err := svc.AssumeRole(&sts.AssumeRoleInput{
		RoleArn:         aws.String(role),
		RoleSessionName: aws.String(name),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to assume role: %w", err)
	}

	return resp.Credentials, nil
}

func getCredEnv(cred *sts.Credentials) []string {
	return []string{
		fmt.Sprintf("AWS_ACCESS_KEY_ID=%s", aws.StringValue(cred.AccessKeyId)),
		fmt.Sprintf("AWS_SECRET_ACCESS_KEY=%s", aws.StringValue(cred.SecretAccessKey)),
		fmt.Sprintf("AWS_SESSION_TOKEN=%s", aws.StringValue(cred.SessionToken)),
	}
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

func (a *App) runTf(cwd string, req *Request, env []string, args ...string) (*exec.Cmd, error) {
	cmd := exec.Command(a.tf, args...) // #nosec
	cmd.Env = env
	cmd.Dir = cwd
	// grab the output pipes so we can prepend the job
	// name before each line so it is easier to discern
	// which message belongs to which job
	stdoutP, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to open stdout pipe: %v", err)
	}
	stderrP, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to open stdout pipe: %v", err)
	}

	err = cmd.Start()
	if err != nil {
		return nil, fmt.Errorf("failed to start process: %s -> %v", a.tf, err)
	}

	go func() {
		err := a.readOutput(req.Name, stdoutP, stderrP)
		if err != nil {
			log.Printf("[ERROR] %v", err)
		}
		err = cmd.Wait()
		if err != nil {
			log.Printf("[ERROR] %v", err)
		}
	}()

	return cmd, nil
}

func (a *App) readOutput(name string, stdout, stderr io.Reader) error {
	stdoutScanner := bufio.NewScanner(stdout)
	stderrScanner := bufio.NewScanner(stderr)

	// In order to read the output pipes we use a bufio.Scanner
	// which by default reads a line on each pass so we start
	// our wrapping func in a go routine for each pipe
	wg := &sync.WaitGroup{}
	wg.Add(2)
	go a.wrapOutput(name, os.Stdout, stdoutScanner, wg)
	go a.wrapOutput(name, os.Stderr, stderrScanner, wg)

	// block until the process has stopped writing to the pipe
	wg.Wait()

	// if something goes wrong return the error to the caller
	if err := stdoutScanner.Err(); err != nil {
		return fmt.Errorf("stdoutScanner failed: %v", err)
	}
	if err := stderrScanner.Err(); err != nil {
		return fmt.Errorf("stderrScanner failed: %v", err)
	}

	return nil
}

func (a *App) wrapOutput(name string, out io.Writer, s *bufio.Scanner, wg *sync.WaitGroup) {
	// Scan pulls one line from the pipe
	// so we can wrap it with the job name
	for s.Scan() {
		_, err := fmt.Fprintf(out, "[%s]: %s\n", name, s.Text())
		if err != nil {
			log.Printf("failed to Fprintf: %v\n", err)
		}
	}

	// decrement the waitgroup
	wg.Done()
}
