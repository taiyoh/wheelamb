package docker

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"log"

	"github.com/docker/docker/api/types"
	"github.com/moby/moby/client"
)

// Docker represents interface for docker operation in wheelamb.
type Docker interface {
	Pull(context.Context, string) error
}

// NewDockerGateway returns Docker operation implementation.
func NewDockerGateway(logLevel string) (Docker, error) {
	cli, err := client.NewEnvClient()
	if err != nil {
		return nil, err
	}
	return &dockerGateway{
		cli:      cli,
		logLevel: logLevel,
	}, nil
}

type dockerGateway struct {
	cli      *client.Client
	logLevel string
}

func (d *dockerGateway) Pull(ctx context.Context, tag string) error {
	body, err := d.cli.ImagePull(ctx, "docker.io/lambci/lambda:"+tag, types.ImagePullOptions{})
	if err != nil {
		return err
	}
	defer body.Close()
	reader := bufio.NewReader(body)
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
