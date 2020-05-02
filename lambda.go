package wheelamb

import "time"

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
	containerID  string
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
