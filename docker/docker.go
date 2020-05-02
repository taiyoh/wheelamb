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
	"strings"
	"sync"
)

const (
	lambciImage    = "docker.io/lambci/lambda"
	defaultNetwork = "default"
)

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

// Docker represents interface for docker operation in wheelamb.
type Docker interface {
	RunImage(context.Context, RunImageConfig) (*ContainerInspect, error)
	KillMulti(context.Context, []string) error
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

	networkName, err = gw.DetectNetwork(context.Background(), networkName)
	if err != nil {
		return nil, err
	}

	gw.networkName = networkName

	return gw, nil
}

type dockerGateway struct {
	apiClient   *apiClient
	logLevel    string
	networkName string
}

func (d *dockerGateway) inspectNetwork(ctx context.Context, name string) error {
	resp, err := d.apiClient.DoRequest(ctx, http.MethodGet, "/networks/"+name)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return unmarshalErrorMessage(resp.Body)
	}
	return nil
}

func (d *dockerGateway) inspectHostConfigFromContainer(ctx context.Context, name string) (*containerHostConfig, error) {
	resp, err := d.apiClient.DoRequest(ctx, http.MethodGet, fmt.Sprintf("/containers/%s/json", name))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case 404:
		return &containerHostConfig{
			NetworkMode: defaultNetwork,
		}, nil
	case 200:
	default:
		return nil, unmarshalErrorMessage(resp.Body)
	}
	body := struct {
		HostConfig *containerHostConfig
	}{}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	return body.HostConfig, nil
}

func (d *dockerGateway) DetectNetwork(ctx context.Context, name string) (string, error) {
	if name != "" && name != defaultNetwork {
		if err := d.inspectNetwork(ctx, name); err != nil {
			return "", err
		}
	}
	if name != "" {
		return name, nil
	}
	hostname, err := os.Hostname()
	if err != nil {
		return "", err
	}
	conf, err := d.inspectHostConfigFromContainer(ctx, hostname)
	if err != nil {
		return "", err
	}
	return conf.NetworkMode, nil
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
		line = bytes.Trim(line, "\r")
		if d.logLevel == "debug" {
			log.Print(string(line)) // TODO
		}
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
	PortBindings  map[string][]containerPortBind
	AutoRemove    bool   // forcely set al true
	NetworkMode   string // forcely set as "bridge"
	RestartPolicy containerRestartPolicy
}

type createContainerConfig struct {
	Env          []string
	Cmd          []string
	Image        string
	ExposedPorts map[string]struct{} `json:",omitempty"` // default: {}
	HostConfig   containerHostConfig
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

	var portBindings map[string][]containerPortBind
	var exposedPorts map[string]struct{}
	if d.networkName == defaultNetwork {
		portBindings = map[string][]containerPortBind{
			"9001/tcp": {{HostPort: "0"}},
		}
		exposedPorts = map[string]struct{}{
			"9001/tcp": {},
		}
	}
	conf := createContainerConfig{
		Env:          envList,
		Image:        lambciImage + ":" + params.Tag,
		Cmd:          []string{params.Handler},
		ExposedPorts: exposedPorts,
		HostConfig: containerHostConfig{
			Binds:        []string{fmt.Sprintf("%s:/var/task:ro,delegated", params.Dir)},
			NetworkMode:  d.networkName,
			PortBindings: portBindings,
			AutoRemove:   true,
			RestartPolicy: containerRestartPolicy{
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
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return "", unmarshalErrorMessage(resp.Body)
	}
	body := struct {
		ID       string `json:"Id"`
		Warnings []string
	}{}
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

	if d.networkName != defaultNetwork {
		return &ContainerInspect{
			ID:   containerID,
			Addr: fmt.Sprintf("%s:9001", params.Name),
		}, nil
	}

	hostConf, err := d.inspectHostConfigFromContainer(ctx, containerID)
	if err != nil {
		return nil, err
	}

	portBind := hostConf.PortBindings["9001/tcp"][0]
	return &ContainerInspect{
		ID:   containerID,
		Addr: fmt.Sprintf("%s:%s", portBind.HostIP, portBind.HostPort),
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
				if d.logLevel == "debug" {
					log.Printf("container:%s closed", id)
				}
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
