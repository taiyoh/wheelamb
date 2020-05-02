package wheelamb

import (
	"strings"
	"time"

	"github.com/taiyoh/wheelamb/docker"
)

// LambdaFunction describes lambda function settings.
// via https://docs.aws.amazon.com/cli/latest/reference/lambda/create-function.html
type LambdaFunction struct {
	CodeSha256   string
	FunctionName string
	CodeSize     int64
	RevisionID   string `json:"RevisionId"` // use uuid
	MemorySize   int64
	FunctionArn  string
	Version      string // "$LATEST",
	Timeout      int64
	LastModified time.Time
	Handler      string
	Runtime      string
	Description  *string
	envs         map[string]string
	inspect      *docker.ContainerInspect
}

// https://github.com/lambci/docker-lambda#docker-tags
var availableTags = map[string]struct{}{
	"nodejs4.3":     {},
	"nodejs6.10":    {},
	"nodejs8.10":    {},
	"nodejs10.x":    {},
	"nodejs12.x":    {},
	"python2.7":     {},
	"python3.6":     {},
	"python3.7":     {},
	"python3.8":     {},
	"ruby2.5":       {},
	"ruby2.7":       {},
	"java8":         {},
	"java11":        {},
	"go1.x":         {},
	"dotnetcore2.0": {},
	"dotnetcore2.1": {},
	"dotnetcore3.1": {},
	// "provided":            struct{}{},
	// "build-nodejs4.3":     struct{}{},
	// "build-nodejs6.10":    struct{}{},
	// "build-nodejs8.10":    struct{}{},
	// "build-nodejs10.x":    struct{}{},
	// "build-nodejs12.x":    struct{}{},
	// "build-python2.7":     struct{}{},
	// "build-python3.6":     struct{}{},
	// "build-python3.7":     struct{}{},
	// "build-python3.8":     struct{}{},
	// "build-ruby2.5":       struct{}{},
	// "build-ruby2.7":       struct{}{},
	// "build-java8":         struct{}{},
	// "build-java11":        struct{}{},
	// "build-go1.x":         struct{}{},
	// "build-dotnetcore2.0": struct{}{},
	// "build-dotnetcore2.1": struct{}{},
	// "build-dotnetcore3.1": struct{}{},
	// "build-provided":      struct{}{},
}

// LambdaRegistry holds lambda function settings in memory.
type LambdaRegistry struct {
	mapping map[string]*LambdaFunction
}

// NewLambdaRegistry returns LambdaRegistry object.
func NewLambdaRegistry() *LambdaRegistry {
	return &LambdaRegistry{
		mapping: make(map[string]*LambdaFunction),
	}
}

// Get returns LambdaFucntion object from given name.
func (r *LambdaRegistry) Get(name string) *LambdaFunction {
	return r.mapping[name]
}

// GetFromARN returns LambdaFunction object from given function arn.
func (r *LambdaRegistry) GetFromARN(arn string) *LambdaFunction {
	// arn:aws:lambda:%s:000000000000:function:%s
	parts := strings.Split(arn, ":")
	for i, p := range []string{"arn", "aws", "lambda", *awsConf.Region, "000000000000", "function"} {
		if parts[i] != p {
			return nil
		}
	}
	return r.mapping[parts[6]]
}

// Register sets LambdaFunction into registry.
func (r *LambdaRegistry) Register(lf *LambdaFunction) {
	r.mapping[lf.FunctionName] = lf
}
