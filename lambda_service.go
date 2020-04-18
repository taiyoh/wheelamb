package wheelamb

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"time"

	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/service/lambda"
	"github.com/docker/docker/api/types"
	"github.com/google/uuid"
	"github.com/moby/moby/client"
)

// LambdaService provides interfaces for operationg lambda functions.
type LambdaService struct {
	cli  *client.Client
	dir  string
	pool map[string]*LambdaFunction
}

// NewLambdaService returns LambdaService object.
func NewLambdaService(dockerHost, dockerVersion string) (*LambdaService, error) {
	cli, err := client.NewClient(dockerHost, dockerVersion, nil, nil)
	if err != nil {
		return nil, err
	}
	dir, err := ioutil.TempDir("", "")
	if err != nil {
		return nil, err
	}
	return &LambdaService{
		cli:  cli,
		dir:  dir,
		pool: map[string]*LambdaFunction{},
	}, nil
}

// Close remove temporary directory for code storage.
func (s *LambdaService) Close() {
	os.RemoveAll(s.dir) // clean up
}

func putZippedCode(d, name string, zippedFile []byte) (size int64, err error) {
	newDir := filepath.Join(d, name)
	switch info, err := os.Stat(newDir); {
	case info != nil:
		return 0, awserr.New(lambda.ErrCodeResourceInUseException, "already created", nil)
	case err != nil:
		if _, ok := err.(*os.PathError); !ok {
			return 0, awserr.New(lambda.ErrCodeResourceInUseException, "unexpected error", err)
		}
	}
	if err := os.MkdirAll(newDir, 0755); err != nil {
		return 0, awserr.New(lambda.ErrCodeServiceException, "failed to create directory", err)
	}
	defer func() {
		if err != nil {
			os.RemoveAll(newDir)
		}
	}()
	zipped, err := base64.StdEncoding.DecodeString(string(zippedFile))
	if err != nil {
		err = awserr.New(lambda.ErrCodeInvalidZipFileException, "unable to decode from base64", err)
		return
	}
	f, err := os.OpenFile(filepath.Join(newDir, "code.zip"), os.O_CREATE|os.O_RDWR, 0755)
	if err != nil {
		err = awserr.New(lambda.ErrCodeServiceException, "failed to put zipfile", err)
		return
	}
	size, err = io.Copy(f, bytes.NewReader(zipped))
	if err != nil {
		err = awserr.New(lambda.ErrCodeServiceException, "failed to put zipfile", err)
	}
	return
}

// Create creates new lambda function.
func (s *LambdaService) Create(ctx context.Context, input *lambda.CreateFunctionInput) (*LambdaFunction, error) {
	if err := input.Validate(); err != nil {
		return nil, err
	}
	if _, ok := availableTags[*input.Runtime]; !ok {
		return nil, awserr.New(lambda.ErrCodeInvalidRuntimeException, "invalid runtime", nil)
	}
	if input.Code == nil || input.Code.ZipFile == nil {
		return nil, awserr.New(lambda.ErrCodeInvalidZipFileException, "requires zipfile", nil)
	}
	name := *input.FunctionName
	size, err := putZippedCode(s.dir, name, input.Code.ZipFile)
	if err != nil {
		return nil, err
	}
	envs := map[string]string{}
	if input.Environment != nil && input.Environment.Variables != nil {
		for k, v := range input.Environment.Variables {
			envs[k] = *v
		}
	}
	sha256Sum := sha256.Sum256(input.Code.ZipFile)
	lf := &LambdaFunction{
		RevisionID:   uuid.New().String(),
		Version:      "$LATEST",
		CodeSha256:   string(sha256Sum[:]),
		LastModified: time.Now().UTC(),
		FunctionName: name,
		FunctionArn:  fmt.Sprintf("arn:aws:lambda:%s:000000000000:function:%s", *awsConf.Region, name),
		MemorySize:   *input.MemorySize,
		Handler:      *input.Handler,
		Runtime:      *input.Runtime,
		Timeout:      *input.Timeout,
		Description:  input.Description,
		CodeSize:     size,
		envs:         envs,
	}
	s.pool[name] = lf
	return lf, nil
}

func invokeFunction(ctx context.Context, cli *client.Client) {
	cli.ContainerStart(ctx, "", types.ContainerStartOptions{})
}

// InvokeSync invokes lambda function with waiting response.
func (s *LambdaService) InvokeSync(ctx context.Context, input *lambda.InvokeInput) (*lambda.InvokeOutput, error) {
	return nil, nil
}

// InvokeAsync invokes lambda function without waiting response.
func (s *LambdaService) InvokeAsync(ctx context.Context, input *lambda.InvokeAsyncInput) (*lambda.InvokeAsyncOutput, error) {
	return nil, nil
}

func (s *LambdaService) find(name string) *LambdaFunction {
	return s.pool[name]
}