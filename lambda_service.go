package wheelamb

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/lambda"
	"github.com/google/uuid"
	"github.com/taiyoh/wheelamb/docker"
)

// LambdaService provides interfaces for operationg lambda functions.
type LambdaService struct {
	docker   docker.Docker
	dir      string
	registry *lambdaRegistry
	session  *session.Session
}

// NewLambdaService returns LambdaService object.
func NewLambdaService(docker docker.Docker, dir string, r *lambdaRegistry) *LambdaService {
	return &LambdaService{
		dir:      dir,
		docker:   docker,
		registry: r,
		session:  session.Must(session.NewSession(awsConf)),
	}
}

// Close closes all lambda function containers.
func (s *LambdaService) Close() error {
	ids := make([]string, 0, len(s.pool))
	for _, lf := range s.pool {
		ids = append(ids, lf.inspect.ID)
	}
	return s.docker.KillMulti(context.Background(), ids)
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
	zreader := bytes.NewReader(zipped)
	zr, err := zip.NewReader(zreader, zreader.Size())
	if err != nil {
		err = awserr.New(lambda.ErrCodeInvalidZipFileException, "unable to read zip code", err)
		return
	}
	for _, f := range zr.File {
		if err = putZippedFile(f, newDir); err != nil {
			err = awserr.New(lambda.ErrCodeInvalidZipFileException, "unable to read zip code", err)
			return
		}
	}
	size = zreader.Size()
	return
}

func putZippedFile(f *zip.File, dest string) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	fileName := filepath.Join(dest, f.Name)
	if f.FileInfo().IsDir() {
		return os.MkdirAll(fileName, f.Mode())
	}
	r := bytes.NewBuffer([]byte{})
	if _, err := r.ReadFrom(rc); err != nil {
		return err
	}
	return ioutil.WriteFile(fileName, r.Bytes(), f.Mode())
}

// Create creates new lambda function.
func (s *LambdaService) Create(ctx context.Context, input *lambda.CreateFunctionInput) (*LambdaFunction, error) {
	if err := input.Validate(); err != nil {
		return nil, err
	}
	if _, ok := availableTags[*input.Runtime]; !ok {
		return nil, awserr.New(lambda.ErrCodeInvalidRuntimeException, "invalid runtime", nil)
	}
	if input.Code.ZipFile == nil {
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
	inspect, err := s.docker.RunImage(ctx, docker.RunImageConfig{
		Name:    "wheelamb-" + name,
		Dir:     filepath.Join(s.dir, name),
		Tag:     *input.Runtime,
		Handler: *input.Handler,
		Envs:    envs,
	})
	if err != nil {
		return nil, awserr.New(lambda.ErrCodeServiceException, "failed to start container", err)
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
		inspect:      inspect,
	}
	s.registry.Register(lf)
	return lf, nil
}

func (s *LambdaService) initCaller(name string) (*lambda.Lambda, error) {
	lf := s.registry.Get(name)
	if lf == nil {
		return nil, awserr.New(lambda.ErrCodeResourceNotFoundException, "function not found", nil)
	}
	conf := aws.NewConfig().WithEndpoint(fmt.Sprintf("http://%s", lf.inspect.Addr))
	return lambda.New(s.session, conf), nil
}

// InvokeSync invokes lambda function with waiting response.
func (s *LambdaService) InvokeSync(ctx context.Context, input *lambda.InvokeInput) (*lambda.InvokeOutput, error) {
	svc, err := s.initCaller(*input.FunctionName)
	if err != nil {
		return nil, err
	}
	return svc.InvokeWithContext(ctx, input)
}

// InvokeAsync invokes lambda function without waiting response.
func (s *LambdaService) InvokeAsync(ctx context.Context, input *lambda.InvokeAsyncInput) (*lambda.InvokeAsyncOutput, error) {
	svc, err := s.initCaller(*input.FunctionName)
	if err != nil {
		return nil, err
	}
	return svc.InvokeAsyncWithContext(ctx, input)
}
