package docker

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
)

const lambciImage = "docker.io/lambci/lambda"

// Docker represents interface for docker operation in wheelamb.
type Docker interface {
	Pull(context.Context, string) error
	RunImage(context.Context, RunImageConfig) (string, error)
}

type apiClient struct {
	basePath   string
	httpClient *http.Client
}

type apiRequest struct {
	url   string
	query url.Values
	body  interface{}
}

func (r *apiRequest) buildURL() string {
	if len(r.query) == 0 {
		return r.url
	}
	if v := r.query.Encode(); v != "" {
		return r.url + "?" + v
	}
	return r.url
}

func (r *apiRequest) buildBody() io.Reader {
	if r.body == nil {
		return nil
	}
	b, err := json.Marshal(r.body)
	if err != nil {
		return nil
	}
	// log.Printf("body: %s", b)
	return bytes.NewBuffer(b)
}

type apiRequestBuilder func(*apiRequest)

func requestQuery(q url.Values) apiRequestBuilder {
	return func(r *apiRequest) {
		r.query = q
	}
}

func requestBody(b interface{}) apiRequestBuilder {
	return func(r *apiRequest) {
		r.body = b
	}
}

func (c *apiClient) DoRequest(ctx context.Context, method, path string, fns ...apiRequestBuilder) (*http.Response, error) {
	r := &apiRequest{
		url: c.basePath + path,
	}
	for _, fn := range fns {
		fn(r)
	}
	reqBody := r.buildBody()
	req, err := http.NewRequestWithContext(ctx, method, r.buildURL(), reqBody)
	if reqBody != nil {
		req.Header.Add("Content-Type", "application/json")
	}
	if err != nil {
		return nil, err
	}
	return c.httpClient.Do(req)
}

func newClient(host string) (*apiClient, error) {
	basePath := "http://localhost/v1.40"
	var tr http.RoundTripper
	switch strs := strings.Split(host, "://"); strs[0] {
	case "unix":
		path := strings.ReplaceAll(host, "unix://", "")
		tr = &http.Transport{
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("unix", path)
			},
		}
	case "tcp":
		path := strings.ReplaceAll(host, "tcp://", "")
		basePath = strings.ReplaceAll(basePath, "localhost", path)
	default:
		return nil, errors.New("invalid host")
	}
	return &apiClient{
		basePath: basePath,
		httpClient: &http.Client{
			Transport: tr,
		},
	}, nil
}

// NewDockerGateway returns Docker operation implementation.
func NewDockerGateway(host, logLevel, networkName string) (Docker, error) {
	cli, err := newClient(host)
	if err != nil {
		return nil, err
	}

	gw := &dockerGateway{
		apiClient: cli,
		logLevel:  logLevel,
	}
	return gw, nil
}

type dockerGateway struct {
	apiClient *apiClient
	logLevel  string
}

func (d *dockerGateway) HasNetwork(ctx context.Context, name string) error {
	resp, err := d.apiClient.DoRequest(ctx, http.MethodGet, "/networks/"+name)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return errors.New("invalid network id")
	}
	return nil
}

func (d *dockerGateway) Pull(ctx context.Context, tag string) error {
	vals := url.Values{}
	vals.Add("fromImage", lambciImage)
	vals.Add("tag", tag)
	resp, err := d.apiClient.DoRequest(ctx, http.MethodPost, "/images/create", requestQuery(vals))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	reader := bufio.NewReader(resp.Body)
	for {
		line, err := reader.ReadBytes('\n')
		if err == io.EOF {
			break
		}
		line = bytes.Trim(line, "\r")
		if d.logLevel == "debug" {
			log.Print(string(line)) // TODO
		}
	}
	return nil
}

type createContainerPortBind struct {
	HostIP   string `json:"HostIp"`
	HostPort string
}

type createContainerRestartPolicy struct {
	Name              string
	MaximumRetryCount int
}

type createContainerHostConfig struct {
	Binds         []string
	PortBindings  map[string][]createContainerPortBind
	AutoRemove    bool   // forcely set al true
	NetworkMode   string // forcely set as "bridge"
	RestartPolicy createContainerRestartPolicy
}

type createContainerConfig struct {
	Env          []string
	Cmd          []string
	Image        string
	ExposedPorts map[string]struct{} `json:",omitempty"` // default: {}
	HostConfig   createContainerHostConfig
}

// RunImageConfig represents parameters to run specified image.
type RunImageConfig struct {
	Name    string
	Envs    map[string]string
	Dir     string
	Tag     string
	Handler string
}

func (d *dockerGateway) RunImage(ctx context.Context, params RunImageConfig) (string, error) {
	envList := []string{
		"DOCKER_LAMBDA_STAY_OPEN=1",
	}
	for k, v := range params.Envs {
		envList = append(envList, k+"="+v)
	}
	conf := createContainerConfig{
		Env:   envList,
		Image: lambciImage + ":" + params.Tag,
		Cmd:   []string{params.Handler},
		ExposedPorts: map[string]struct{}{
			"9001/tcp": {},
		},
		HostConfig: createContainerHostConfig{
			Binds:       []string{fmt.Sprintf("%s:/var/task:ro,delegated", params.Dir)},
			NetworkMode: "default",
			PortBindings: map[string][]createContainerPortBind{
				"9001/tcp": {{HostPort: "0"}},
			},
			AutoRemove: true,
			RestartPolicy: createContainerRestartPolicy{
				Name: "no",
			},
		},
	}

	vals := url.Values{}
	vals.Add("name", params.Name)
	resp, err := d.apiClient.DoRequest(ctx, http.MethodPost,
		"/containers/create", requestQuery(vals), requestBody(conf))
	if err != nil {
		return "", err
	}
	body := struct {
		Message  string `json:"message"`
		ID       string `json:"Id"`
		Warnings []string
	}{}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", err
	}
	resp.Body.Close()
	// log.Printf("body: %#v", body)
	if body.Message != "" {
		return body.ID, fmt.Errorf(body.Message)
	}
	if len(body.Warnings) > 0 {
		return body.ID, fmt.Errorf("warnings: %v", body.Warnings)
	}
	resp, err = d.apiClient.DoRequest(ctx, http.MethodPost,
		fmt.Sprintf("/containers/%s/start", body.ID))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			return "", err
		}
		return "", fmt.Errorf("failed to start container: %s", b)
	}
	return body.ID, nil
}
