package wheelamb

import (
	"os"

	"github.com/aws/aws-sdk-go/aws"
)

var awsConf *aws.Config

func init() {
	awsConf = aws.NewConfig().WithRegion(os.Getenv("AWS_REGION"))
}
