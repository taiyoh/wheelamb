package wheelamb

import (
	"context"

	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/lambda"
)

// SourceMappingService provides interfaces for operating EventSourceMapping for lambda.
type SourceMappingService struct {
	registry *LambdaRegistry
	session  *session.Session
}

// CreateEventSourceMapping creates mappings for lambda invokation from sqs or kinesis streams.
func (s *SourceMappingService) CreateEventSourceMapping(ctx context.Context, input *lambda.CreateEventSourceMappingInput) error {
	return nil
}
