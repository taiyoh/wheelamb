package wheelamb

import (
	"encoding/base64"
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"

	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/service/lambda"
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
