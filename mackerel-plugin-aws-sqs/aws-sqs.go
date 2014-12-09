package main

import (
	"errors"
	"flag"
	"log"
	"os"
	"strings"
	"time"

	"github.com/crowdmob/goamz/aws"
	"github.com/crowdmob/goamz/cloudwatch"
	mp "github.com/mackerelio/go-mackerel-plugin"
)

var graphdef map[string](mp.Graphs) = map[string](mp.Graphs){
	"sqs.Messages": mp.Graphs{
		Label: "SQS NumberOfMessagesSent",
		Unit:  "integer",
		Metrics: [](mp.Metrics){
			mp.Metrics{Name: "NumberOfMessagesSent", Label: "NumberOfMessagesSent"},
			mp.Metrics{Name: "NumberOfMessagesReceived", Label: "NumberOfMessagesReceived"},
			mp.Metrics{Name: "NumberOfEmptyReceives", Label: "NumberOfEmptyReceives"},
			mp.Metrics{Name: "NumberOfMessagesDeleted", Label: "NumberOfMessagesDeleted"},
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
		Label: "SQS Queue Status",
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
	QueueName       string
}

func GetLastPoint(cloudWatch *cloudwatch.CloudWatch, dimension *cloudwatch.Dimension, metricName string) (float64, error) {
	now := time.Now()

	statistics := []string{"Sum"}
	if strings.HasPrefix(metricName, "Approximate") {
		statistics = []string{"Average"}
	}

	response, err := cloudWatch.GetMetricStatistics(&cloudwatch.GetMetricStatisticsRequest{
		Dimensions: []cloudwatch.Dimension{*dimension},
		StartTime:  now.Add(time.Duration(180) * time.Second * -1), // 3 min (to fetch at least 1 data-point)
		EndTime:    now,
		MetricName: metricName,
		Period:     60,
		Statistics: statistics,
		Namespace:  "AWS/SQS",
	})
	if err != nil {
		return 0, err
	}

	datapoints := response.GetMetricStatisticsResult.Datapoints
	if len(datapoints) == 0 {
		return 0, errors.New("fetched no datapoints")
	}

	latest := time.Unix(0, 0)
	var latestVal float64
	for _, dp := range datapoints {
		if dp.Timestamp.Before(latest) {
			continue
		}

		latest = dp.Timestamp
		latestVal = dp.Average
	}

	return latestVal, nil
}

func (p SQSPlugin) FetchMetrics() (map[string]float64, error) {
	auth, err := aws.GetAuth(p.AccessKeyId, p.SecretAccessKey, "", time.Now())
	if err != nil {
		return nil, err
	}

	cloudWatch, err := cloudwatch.NewCloudWatch(auth, aws.Regions[p.Region].CloudWatchServicepoint)
	if err != nil {
		return nil, err
	}

	stat := make(map[string]float64)

	perQueueName := &cloudwatch.Dimension{
		Name:  "QueueName",
		Value: p.QueueName,
	}

	for _, met := range [...]string{
		"NumberOfMessagesSent",
		"SentMessageSize",
		"NumberOfMessagesReceived",
		"NumberOfEmptyReceives",
		"NumberOfMessagesDeleted",
		"ApproximateNumberOfMessagesDelayed",
		"ApproximateNumberOfMessagesVisible",
		"ApproximateNumberOfMessagesNotVisible",
	} {
		v, err := GetLastPoint(cloudWatch, perQueueName, met)
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
	optQueueName := flag.String("queuename", "", "QueueName")
	optTempfile := flag.String("tempfile", "", "Temp file name")
	flag.Parse()

	var sqs SQSPlugin

	if *optRegion == "" {
		sqs.Region = aws.InstanceRegion()
	} else {
		sqs.Region = *optRegion
	}

	sqs.AccessKeyId = *optAccessKeyId
	sqs.SecretAccessKey = *optSecretAccessKey
	sqs.QueueName = *optQueueName

	helper := mp.NewMackerelPlugin(sqs)
	if *optTempfile != "" {
		helper.Tempfile = *optTempfile
	} else {
		helper.Tempfile = "/tmp/mackerel-plugin-sqs"
	}

	if os.Getenv("MACKEREL_AGENT_PLUGIN_META") != "" {
		helper.OutputDefinitions()
	} else {
		helper.OutputValues()
	}
}
