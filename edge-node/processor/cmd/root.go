package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/tinhtn1508/edge-computing-for-monitor/edge-node/processor/config"
	"github.com/tinhtn1508/edge-computing-for-monitor/edge-node/processor/core"
	"github.com/tinhtn1508/edge-computing-for-monitor/go-lib/influxdb"
	"github.com/tinhtn1508/edge-computing-for-monitor/go-lib/kafka"
	"github.com/tinhtn1508/edge-computing-for-monitor/go-lib/rmq"
	"go.uber.org/zap"
)

var log *zap.SugaredLogger
var globalContext context.Context

var rootCmd = &cobra.Command{
	Use: "",
	Run: func(cmd *cobra.Command, args []string) {
		log.Infof("hello")
		kafkaClient := kafka.NewTcpKafkaConnector(kafka.TcpKafkaConnectorDeps{
			Log:     log,
			Ctx:     globalContext,
			Timeout: 500 * time.Millisecond,
			Host:    config.GetConfig().KafkaMgtConfig.Host,
		})
		if err := kafkaClient.Open(); err != nil {
			log.Fatalf("error while opening kafka connection: %s", err)
		}
		if err := kafkaClient.CreateTopics(config.GetConfig().KafkaMgtConfig.Topic, 1, 1); err != nil {
			log.Fatalf("error while creating kafka topic: %s", err)
		}
		kafkaWritter := kafka.NewSimpleProducer(kafka.SimpleProducerConfig{
			Log:       log,
			Ctx:       globalContext,
			Topic:     config.GetConfig().KafkaMgtConfig.Topic,
			Brokers:   config.GetConfig().KafkaMgtConfig.Brokers,
			Batchsize: 1,
			Timeout:   config.GetConfig().KafkaMgtConfig.WriteTimeout,
		})
		if err := kafkaWritter.Start(); err != nil {
			log.Fatalf("Cannot initialize kafka writter, error: %s", err)
		}

		// KafkaErrorWriter
		if err := kafkaClient.CreateTopics("error", 1, 1); err != nil {
			log.Fatalf("error while creating kafka error topic: %s", err)
		}
		kafkaErrorWritter := kafka.NewSimpleProducer(kafka.SimpleProducerConfig{
			Log:       log,
			Ctx:       globalContext,
			Topic:     config.GetConfig().KafkaErrConfig.Topic,
			Brokers:   config.GetConfig().KafkaErrConfig.Brokers,
			Batchsize: 1,
			Timeout:   config.GetConfig().KafkaErrConfig.WriteTimeout,
		})
		if err := kafkaErrorWritter.Start(); err != nil {
			log.Fatalf("Cannot initialize kafka writter, error: %s", err)
		}

		influxdbClient := influxdb.NewHTTPInfluxDBConnector(influxdb.HTTPInfluxDBConnectorDeps{
			Log:     log,
			Ctx:     globalContext,
			Timeout: 500 * time.Millisecond,
			Host:    config.GetConfig().InfluxDBConfig.Host,
			Port:    config.GetConfig().InfluxDBConfig.Port,
		})

		if err := influxdbClient.Open(); err != nil {
			log.Fatalf("error while opening influxdb connection: %s", err)
		}

		if err := influxdbClient.CreateNewDB("mytest"); err != nil {
			log.Fatalf("error while creating new influxdb, %s", err)
		}

		influxdbWriter := influxdb.NewSimpleWriter(influxdb.WriterConfig{
			Log:       log,
			Ctx:       globalContext,
			Batchsize: config.GetConfig().InfluxDBConfig.BatchSize,
			Duration:  config.GetConfig().InfluxDBConfig.BatchTime,
			DBName:    "mytest",
		})
		influxdbWriter.Init(influxdbClient.GetClient())

		processor := core.NewCoreProcessor(core.CoreProcessorConfig{
			log,
			config.GetConfig().CoreConfig,
			kafkaWritter.Produce,
			kafkaErrorWritter.Produce,
			influxdbWriter.Write,
			"measurement",
			config.GetConfig().EdgeNodeName,
		})
		for _, q := range config.GetConfig().RMQConfig.Queues {
			processor.AddConsumingTask(q)
		}
		consumer, err := rmq.NewTopicConsumer(rmq.RabbitMQConsumerConfig{
			Log:              log,
			ServerURL:        config.GetConfig().RMQConfig.GetUrl(),
			Exchange:         config.GetConfig().RMQConfig.Exchange,
			QueuesProcessors: processor.GetHandlerMap(),
		})

		if err != nil {
			log.Fatalf("Error while creating consumer: %s", err)
		}
		consumer.Start()
		defer consumer.Stop()
		processor.Start()
		defer processor.Stop()
		time.Sleep(60 * time.Second)
	},
}

func init() {
	prepareLogger()
	log.Infof("Run program with the config: %+v", *config.GetConfig())

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGKILL, syscall.SIGTERM)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		sig := <-signals
		fmt.Println("Got signal: ", sig)
		cancel()
		os.Exit(0)
	}()

	globalContext = ctx
}

// Execute starts the tool up
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		log.Errorf("%s", err)
		os.Exit(1)
	}
}

func prepareLogger() {
	logger, _ := zap.NewDevelopment()
	log = logger.Sugar()
	log.Info("Log is prepared in development mode")
}
