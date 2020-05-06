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
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const (
	lambciImage    = "docker.io/lambci/lambda"
	defaultNetwork = "wheelamb_default"
)

type logger struct {
	level string
}

func (l *logger) Debug(msg string, args ...interface{}) {
	if l.level != "debug" {
		return
	}
	log.Printf(msg, args...)
}

type messageForResponseError struct {
	Message string `json:"message"`
}

func (m messageForResponseError) Error() string {
	return m.Message
}

func unmarshalErrorMessage(body io.ReadCloser) error {
	r := messageForResponseError{}
	if err := json.NewDecoder(body).Decode(&r); err != nil {
		return err
	}
	return r
}

type idResponse struct {
	ID       string `json:"Id"`
	Warnings []string
}

// Docker represents interface for docker operation in wheelamb.
type Docker interface {
	RunImage(context.Context, RunImageConfig) (*ContainerInspect, error)
	KillMulti(context.Context, []string) error
}

type apiClient struct {
	basePath   string
	logger     *logger
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

func (r *apiRequest) buildBody() *bytes.Buffer {
	if r.body == nil {
		return nil
	}
	b, err := json.Marshal(r.body)
	if err != nil {
		return nil
	}
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

	url := r.buildURL()
	c.logger.Debug("[%s] %s", method, url)
	var rb io.Reader
	if reqBody := r.buildBody(); reqBody != nil {
		rb = reqBody
		c.logger.Debug(">>  body: %s", reqBody.String())
	}
	req, err := http.NewRequestWithContext(ctx, method, url, rb)
	if rb != nil {
		req.Header.Add("Content-Type", "application/json")
	}
	if err != nil {
		return nil, err
	}
	return c.httpClient.Do(req)
}

func newClient(host string, logger *logger) (*apiClient, error) {
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
		logger:   logger,
		httpClient: &http.Client{
			Transport: tr,
		},
	}, nil
}

// NewDockerGateway returns Docker operation implementation.
func NewDockerGateway(host, logLevel string) (Docker, error) {
	l := &logger{
		level: logLevel,
	}
	cli, err := newClient(host, l)
	if err != nil {
		return nil, err
	}

	gw := &dockerGateway{
		apiClient: cli,
		logger:    l,
	}

	hostname, err := os.Hostname()
	if err != nil {
		return nil, err
	}
	conf, err := gw.inspectHostConfigFromContainer(context.Background(), hostname)
	if err != nil {
		return nil, err
	}

	gw.networkName = conf.NetworkMode
	for _, m := range conf.Mounts {
		if m.Type == "volume" && m.Destination == "/var/task" {
			gw.volume = m.Name
		}
	}

	return gw, nil
}

type dockerGateway struct {
	apiClient   *apiClient
	logger      *logger
	networkName string
	volume      string
}

func (d *dockerGateway) inspectHostConfigFromContainer(ctx context.Context, name string) (*containerHostConfig, error) {
	resp, err := d.apiClient.DoRequest(ctx, http.MethodGet, fmt.Sprintf("/containers/%s/json", name))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case 404:
		return &containerHostConfig{NetworkMode: ""}, nil
	case 200:
	default:
		return nil, unmarshalErrorMessage(resp.Body)
	}
	body := struct {
		HostConfig *containerHostConfig
		Mounts     []*containerMount
	}{}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	body.HostConfig.Mounts = body.Mounts
	return body.HostConfig, nil
}

func (d *dockerGateway) pullImage(ctx context.Context, tag string) error {
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
		d.logger.Debug(string(bytes.Trim(line, "\r")))
	}
	return nil
}

type containerPortBind struct {
	HostIP   string `json:"HostIp"`
	HostPort string
}

type containerRestartPolicy struct {
	Name              string
	MaximumRetryCount int
}

type containerHostConfig struct {
	Binds         []string
	Links         []string                       `json:",omitempty"`
	PortBindings  map[string][]containerPortBind `json:",omitempty"`
	AutoRemove    bool                           // forcely set al true
	NetworkMode   string                         // forcely set as "bridge"
	RestartPolicy containerRestartPolicy
	Mounts        []*containerMount `json:",omitempty"`
}

type containerMount struct {
	Type        string // "bind" "volume" "tmpfs" "npipe"
	Name        string
	Source      string
	Destination string
	Driver      string
}

type containerNetworkConfig struct {
	EndpointsConfig map[string]containerEndpointsConfig
}

type containerEndpointsConfig struct {
	Aliases []string
}

type createContainerConfig struct {
	Env              []string
	Cmd              []string
	Image            string
	ExposedPorts     map[string]struct{} `json:",omitempty"` // default: {}
	HostConfig       containerHostConfig
	NetworkingConfig containerNetworkConfig
}

// RunImageConfig represents parameters to run specified image.
type RunImageConfig struct {
	Name    string
	Envs    map[string]string
	Dir     string
	Tag     string
	Handler string
}

func (d *dockerGateway) createContainer(ctx context.Context, params RunImageConfig) (string, error) {
	envList := []string{
		"DOCKER_LAMBDA_STAY_OPEN=1",
	}
	for k, v := range params.Envs {
		envList = append(envList, k+"="+v)
	}

	conf := createContainerConfig{
		Env:   envList,
		Image: lambciImage + ":" + params.Tag,
		Cmd:   []string{filepath.Join(params.Dir, params.Handler)},
		HostConfig: containerHostConfig{
			Binds:       []string{fmt.Sprintf("%s:/var/task:ro,delegated", d.volume)},
			NetworkMode: d.networkName,
			AutoRemove:  true,
			RestartPolicy: containerRestartPolicy{
				Name: "no",
			},
		},
		NetworkingConfig: containerNetworkConfig{
			EndpointsConfig: map[string]containerEndpointsConfig{
				d.networkName: {
					Aliases: []string{params.Name},
				},
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
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return "", unmarshalErrorMessage(resp.Body)
	}
	body := idResponse{}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", err
	}
	// log.Printf("body: %#v", body)
	if len(body.Warnings) > 0 {
		return body.ID, fmt.Errorf("warnings: %v", body.Warnings)
	}
	return body.ID, nil
}

func (d *dockerGateway) startContainer(ctx context.Context, contanierID string) error {
	resp, err := d.apiClient.DoRequest(ctx, http.MethodPost,
		fmt.Sprintf("/containers/%s/start", contanierID))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return unmarshalErrorMessage(resp.Body)
	}
	return nil
}

// ContainerInspect represents container information.
type ContainerInspect struct {
	ID   string
	Addr string
}

func (d *dockerGateway) RunImage(ctx context.Context, params RunImageConfig) (*ContainerInspect, error) {
	if err := d.pullImage(ctx, params.Tag); err != nil {
		return nil, err
	}

	containerID, err := d.createContainer(ctx, params)
	if err != nil {
		return nil, err
	}

	if err := d.startContainer(ctx, containerID); err != nil {
		return nil, err
	}

	return &ContainerInspect{
		ID:   containerID,
		Addr: fmt.Sprintf("%s:9001", params.Name),
	}, nil
}

type errList []error

func (l errList) Error() string {
	return fmt.Sprintf("error found: %#v", l)
}

func (l errList) Errors() []error {
	return l
}

func (d *dockerGateway) KillMulti(ctx context.Context, ids []string) error {
	semaphore := make(chan struct{}, 3)
	var errs errList
	wg := &sync.WaitGroup{}
	for _, id := range ids {
		semaphore <- struct{}{}
		wg.Add(1)
		go func(id string) {
			defer func() {
				wg.Done()
				<-semaphore
			}()
			resp, err := d.apiClient.DoRequest(ctx, http.MethodPost,
				fmt.Sprintf("/containers/%s/kill", id))
			if err != nil {
				errs = append(errs, err)
				return
			}
			defer resp.Body.Close()
			if resp.StatusCode < 300 {
				d.logger.Debug("container:%s closed", id)
				return
			}
			switch b, err := ioutil.ReadAll(resp.Body); {
			case err != nil:
				errs = append(errs, err)
			default:
				errs = append(errs, fmt.Errorf("failed to kill container: %s, err: %s", id, b))
			}
		}(id)
	}
	wg.Wait()
	return errs
}
