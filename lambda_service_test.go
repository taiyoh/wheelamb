package wheelamb

import (
	"context"
	"encoding/base64"
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/request"
	"github.com/aws/aws-sdk-go/service/lambda"
	"github.com/taiyoh/wheelamb/docker"
)

func TestPutZippedCode(t *testing.T) {
	for _, tt := range []struct {
		label    string
		init     func(string)
		data     []byte
		expected string
	}{
		{
			label: "already created",
			init: func(d string) {
				os.MkdirAll(filepath.Join(d, "foobar"), 0755)
			},
			data:     []byte("hogefuga"),
			expected: lambda.ErrCodeResourceInUseException,
		},
		{
			label:    "invalid zipped body",
			data:     []byte("aaaii"),
			expected: lambda.ErrCodeInvalidZipFileException,
		},
	} {
		t.Run(tt.label, func(t *testing.T) {
			dir, err := ioutil.TempDir("", "")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { os.RemoveAll(dir) })
			if tt.init != nil {
				tt.init(dir)
			}
			size, err := putZippedCode(dir, "foobar", tt.data)
			if err == nil {
				t.Fatal("error should exists")
			}
			if e, ok := err.(awserr.Error); !ok || e.Code() != tt.expected {
				t.Fatal(err)
			}
			if size != 0 {
				t.Errorf("size: %d != 0", size)
			}
		})
	}
	t.Run("success", func(t *testing.T) {
		dir, err := ioutil.TempDir("", "")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.RemoveAll(dir) })
		data, err := ioutil.ReadFile(filepath.Join("testdata", "fake.zip"))
		if err != nil {
			t.Fatal(err)
		}
		size, err := putZippedCode(dir, "foobar", []byte(base64.StdEncoding.EncodeToString(data)))
		if err != nil {
			t.Error("unexpected error ", err)
		}
		if size != 1097735 {
			t.Errorf("size: %d != 1097735", size)
		}
	})
}

type dockerGatewayMock struct{}

func (dockerGatewayMock) Pull(context.Context, string) error {
	return nil
}

func (dockerGatewayMock) RunImage(context.Context, docker.RunImageConfig) (string, error) {
	return "", nil
}

func TestServiceCreate(t *testing.T) {
	dir, err := ioutil.TempDir("", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })

	svc := NewLambdaService(&dockerGatewayMock{}, dir)
	codeZipped, _ := ioutil.ReadFile(filepath.Join("testdata", "fake.zip"))
	for _, tt := range []struct {
		label       string
		input       *lambda.CreateFunctionInput
		expectedErr func(error) bool
	}{
		{
			label: "no parameters given",
			input: &lambda.CreateFunctionInput{},
			expectedErr: func(err error) bool {
				_, ok := err.(request.ErrInvalidParams)
				return ok
			},
		},
		{
			label: "invalid runtime",
			input: &lambda.CreateFunctionInput{
				Code: &lambda.FunctionCode{
					ZipFile: codeZipped,
				},
				FunctionName: aws.String("mytest"),
				Handler:      aws.String("fake"),
				MemorySize:   aws.Int64(128),
				Timeout:      aws.Int64(16),
				Role:         aws.String("foobar"),
				Runtime:      aws.String("go1.14"),
			},
			expectedErr: func(err error) bool {
				be, ok := err.(awserr.Error)
				if !ok {
					return false
				}
				return be.Code() == lambda.ErrCodeInvalidRuntimeException
			},
		},
		{
			label: "s3 assigns for code",
			input: &lambda.CreateFunctionInput{
				Code: &lambda.FunctionCode{
					S3Bucket: aws.String("aaaaa"),
					S3Key:    aws.String("iiiii"),
				},
				FunctionName: aws.String("mytest"),
				Handler:      aws.String("fake"),
				MemorySize:   aws.Int64(128),
				Timeout:      aws.Int64(16),
				Role:         aws.String("foobar"),
				Runtime:      aws.String("go1.x"),
			},
			expectedErr: func(err error) bool {
				be, ok := err.(awserr.Error)
				if !ok {
					return false
				}
				return be.Code() == lambda.ErrCodeInvalidZipFileException
			},
		},
	} {
		t.Run(tt.label, func(t *testing.T) {
			fn, err := svc.Create(context.Background(), tt.input)
			if fn != nil {
				t.Error("unexpected lambdaFunction captured")
			}
			if err == nil {
				t.Fatal("error should exists")
			}
			if !tt.expectedErr(err) {
				t.Errorf("unexpected error captured: %#v", err)
			}
		})
	}

	t.Run("success", func(t *testing.T) {
		if len(svc.pool) > 0 {
			t.Errorf("pool should be empty: %#v", svc.pool)
		}
		fn, err := svc.Create(context.Background(), &lambda.CreateFunctionInput{
			Code: &lambda.FunctionCode{
				ZipFile: []byte(base64.StdEncoding.EncodeToString(codeZipped)),
			},
			FunctionName: aws.String("mytest"),
			Handler:      aws.String("fake"),
			MemorySize:   aws.Int64(128),
			Timeout:      aws.Int64(16),
			Role:         aws.String("foobar"),
			Runtime:      aws.String("go1.x"),
			Environment: &lambda.Environment{
				Variables: map[string]*string{
					"hoge": aws.String("fuga"),
				},
			},
		})
		if err != nil {
			t.Errorf("unexpected error captured: %#v", err)
		}
		if fn == nil {
			t.Error("lambdaFunction should exists")
		}
		info, err := os.Stat(filepath.Join(svc.dir, "mytest", "code.zip"))
		if err != nil {
			t.Errorf("caught error: %v", err)
		}
		if size := info.Size(); size != 1097735 {
			t.Errorf("size: %d != 1097735", size)
		}
		val, ok := svc.pool["mytest"]
		if !ok {
			t.Error("mytest not registered")
		}
		if fn != val {
			t.Errorf("wrong pointer detected")
		}
	})
}
