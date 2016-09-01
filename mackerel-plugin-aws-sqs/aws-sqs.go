package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/ec2metadata"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/cloudwatch"

	mp "github.com/mackerelio/go-mackerel-plugin"
)

var graphdef map[string](mp.Graphs) = map[string](mp.Graphs){
	"sqs.Messages": mp.Graphs{
		Label: "SQS Message metrics",
		Unit:  "integer",
		Metrics: [](mp.Metrics){
			mp.Metrics{Name: "NumberOfMessagesSent", Label: "NumberOfMessagesSent"},
			mp.Metrics{Name: "NumberOfMessagesReceived", Label: "NumberOfMessagesReceived"},
			mp.Metrics{Name: "NumberOfMessagesDeleted", Label: "NumberOfMessagesDeleted"},
			mp.Metrics{Name: "NumberOfEmptyReceives", Label: "NumberOfEmptyReceives"},
		},
	},
	"sqs.MessageSize": mp.Graphs{
		Label: "SQS Message Size",
		Unit:  "bytes",
		Metrics: [](mp.Metrics){
			mp.Metrics{Name: "SentMessageSize", Label: "SentMessageSize"},
		},
	},
	"sqs.Queue": mp.Graphs{
		Label: "SQS Approximate Message Stats",
		Unit:  "integer",
		Metrics: [](mp.Metrics){
			mp.Metrics{Name: "ApproximateNumberOfMessagesDelayed", Label: "ApproximateNumberOfMessagesDelayed"},
			mp.Metrics{Name: "ApproximateNumberOfMessagesVisible", Label: "ApproximateNumberOfMessagesVisible"},
			mp.Metrics{Name: "ApproximateNumberOfMessagesNotVisible", Label: "ApproximateNumberOfMessagesNotVisible"},
		},
	},
}

type SQSPlugin struct {
	Region          string
	AccessKeyId     string
	SecretAccessKey string
	Credentials     *credentials.Credentials
	QueueName       string
	CloudWatch      *cloudwatch.CloudWatch
}

func getLastPoint(cloudWatch *cloudwatch.CloudWatch, dimension *cloudwatch.Dimension, metricName string) (float64, error) {
	now := time.Now()

	unit := "Count"
	statistics := []*string{}
	switch {
	case metricName == "SentMessageSize":
		unit = "Bytes"
		//statistics = []*string{"Minimum, Maximum, Average, Count"}
		statistics = []*string{aws.String("Average")}
	case strings.HasPrefix(metricName, "Approximate"):
		statistics = []*string{aws.String("Average")}
	default:
		statistics = []*string{aws.String("Sum")}
	}

	response, err := cloudWatch.GetMetricStatistics(&cloudwatch.GetMetricStatisticsInput{
		StartTime:  aws.Time(now.Add(time.Duration(300) * time.Second * -1)),
		EndTime:    aws.Time(now),
		Period:     aws.Int64(60),
		Namespace:  aws.String("AWS/SQS"),
		Dimensions: []*cloudwatch.Dimension{dimension},
		MetricName: aws.String(metricName),
		Unit:       aws.String(unit),
		Statistics: statistics,
	})
	if err != nil {
		return 0, err
	}

	datapoints := response.Datapoints
	if len(datapoints) == 0 {
		return 0, errors.New("fetched no datapoints")
	}

	latest := time.Unix(0, 0)
	var latestVal float64
	for _, dp := range datapoints {
		if dp.Timestamp.Before(latest) {
			continue
		}

		latest = *dp.Timestamp
		switch {
		case metricName == "SentMessageSize":
			latestVal = *dp.Average
		case strings.HasPrefix(metricName, "Approximate"):
			latestVal = *dp.Average
		default:
			latestVal = *dp.Sum
		}
	}

	return latestVal, nil
}

func (p SQSPlugin) FetchMetrics() (map[string]float64, error) {
	if p.QueueName == "" {
		return nil, errors.New("no queuename")
	}

	stat := make(map[string]float64)

	p.CloudWatch = cloudwatch.New(session.New(&aws.Config{Credentials: p.Credentials, Region: &p.Region}))
	perQueueName := &cloudwatch.Dimension{
		Name:  aws.String("QueueName"),
		Value: aws.String(p.QueueName),
	}

	for _, met := range [...]string{
		"SentMessageSize",
		"NumberOfMessagesSent",
		"NumberOfMessagesReceived",
		"NumberOfEmptyReceives",
		"NumberOfMessagesDeleted",
		"ApproximateNumberOfMessagesDelayed",
		"ApproximateNumberOfMessagesVisible",
		"ApproximateNumberOfMessagesNotVisible",
	} {
		v, err := getLastPoint(p.CloudWatch, perQueueName, met)
		if err == nil {
			stat[met] = v
		} else {
			log.Printf("%s: %s", met, err)
		}
	}

	return stat, nil
}

func (p SQSPlugin) GraphDefinition() map[string](mp.Graphs) {
	return graphdef
}

func main() {
	optRegion := flag.String("region", "", "AWS Region")
	optAccessKeyId := flag.String("access-key-id", "", "AWS Access Key ID")
	optSecretAccessKey := flag.String("secret-access-key", "", "AWS Secret Access Key")
	optQueueName := flag.String("queuename", "", "Queue Name")
	optTempfile := flag.String("tempfile", "", "Temp file name")
	flag.Parse()

	var sqs SQSPlugin

	sqs.AccessKeyId = *optAccessKeyId
	sqs.SecretAccessKey = *optSecretAccessKey
	sqs.Region = *optRegion
	sqs.QueueName = *optQueueName

	if *optRegion == "" {
		// get metadata in ec2 instance
		ec2MC := ec2metadata.New(session.New())
		sqs.Region, _ = ec2MC.Region()
	}

	helper := mp.NewMackerelPlugin(sqs)
	if *optTempfile != "" {
		helper.Tempfile = *optTempfile
	} else {
		helper.Tempfile = fmt.Sprintf("/tmp/mackerel-plugin-aws-sqs-%s", *optQueueName)
	}

	if os.Getenv("MACKEREL_AGENT_PLUGIN_META") != "" {
		helper.OutputDefinitions()
	} else {
		helper.OutputValues()
	}
}
