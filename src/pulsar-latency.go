package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/apache/pulsar-client-go/pulsar"
)

const (
	latencyBudget = 2400 // in Millisecond integer, will convert to time.Duration in evaluation
	failedLatency = 100 * time.Second
)

var (
	clients = make(map[string]pulsar.Client)
)

type MsgResult struct {
	InOrderDelivery bool
	Latency         time.Duration
	SentTime        time.Time
}

// PubSubLatency the latency including successful produce and consume of a message
func PubSubLatency(tokenStr, uri, topicName, msgPrefix string, payloads [][]byte) (MsgResult, error) {
	// uri is in the form of pulsar+ssl://useast1.gcp.kafkaesque.io:6651
	client, ok := clients[uri]
	if !ok {

		// Configuration variables pertaining to this consumer
		// RHEL CentOS:
		trustStore := AssignString(GetConfig().PulsarPerfConfig.TrustStore, "/etc/ssl/certs/ca-bundle.crt")
		// Debian Ubuntu:
		// trustStore := '/etc/ssl/certs/ca-certificates.crt'
		// OSX:
		// Export the default certificates to a file, then use that file:
		// security find-certificate -a -p /System/Library/Keychains/SystemCACertificates.keychain > ./ca-certificates.crt
		// trust_certs='./ca-certificates.crt'

		token := pulsar.NewAuthenticationToken(tokenStr)

		var err error
		client, err = pulsar.NewClient(pulsar.ClientOptions{
			URL:                   uri,
			Authentication:        token,
			TLSTrustCertsFilePath: trustStore,
		})

		if err != nil {
			return MsgResult{Latency: failedLatency}, err
		}
		clients[uri] = client
	}

	// it is important to close client after close of producer/consumer
	// defer client.Close()

	// Use the client to instantiate a producer
	producer, err := client.CreateProducer(pulsar.ProducerOptions{
		Topic: topicName,
	})

	if err != nil {
		// we guess something could have gone wrong if producer cannot be created
		client.Close()
		delete(clients, uri)
		return MsgResult{Latency: failedLatency}, err
	}

	defer producer.Close()

	subscriptionName := "latency-measure"
	consumer, err := client.Subscribe(pulsar.ConsumerOptions{
		Topic:                       topicName,
		SubscriptionName:            subscriptionName,
		Type:                        pulsar.Exclusive,
		SubscriptionInitialPosition: pulsar.SubscriptionPositionLatest,
	})

	if err != nil {
		defer client.Close() //must defer to allow producer to be closed first
		delete(clients, uri)
		return MsgResult{Latency: failedLatency}, err
	}
	defer consumer.Close()

	// the original sent time to notify the receiver for latency calculation
	//timeCounter := make(chan time.Time, 1)

	// notify the main thread with the latency to complete the exit
	completeChan := make(chan MsgResult, 1)

	// error report channel
	errorChan := make(chan error, 1)

	// payloadStr := "measure-latency123" + time.Now().Format(time.UnixDate)
	receivedCount := len(payloads)
	sentPayloads := make(map[string]*MsgResult, receivedCount)

	go func() {

		lastMessageIndex := -1 // to track the message delivery order
		for receivedCount > 0 {
			cCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			msg, err := consumer.Receive(cCtx)
			if err != nil {
				receivedCount = 0 // play safe?
				errorChan <- fmt.Errorf("consumer Receive() error: %v", err)
				break
			}
			receivedTime := time.Now()
			receivedStr := string(msg.Payload())
			currentMsgIndex := GetMessageId(msgPrefix, receivedStr)
			if result, ok := sentPayloads[receivedStr]; ok {
				receivedCount--
				result.Latency = receivedTime.Sub(result.SentTime)
				if currentMsgIndex > lastMessageIndex {
					result.InOrderDelivery = true
					lastMessageIndex = currentMsgIndex
				}
				/**
				select {
				case sentTime := <-timeCounter:
					completeChan <- time.Now().Sub(sentTime)

				case <-time.Tick(5 * time.Second):
					// this is impossible case that producer must have sent signal
					errMsg := fmt.Sprintf("consumer received message, but timed out on producer report time")
					errorChan <- errors.New(errMsg)
				}
				**/
			}
			consumer.Ack(msg)
			log.Printf("consumer index received %d payload size %d\n", currentMsgIndex, len(receivedStr))
		}

		//successful case all message received
		if receivedCount == 0 {
			var total time.Duration
			inOrder := true
			for _, v := range sentPayloads {
				total += v.Latency
				inOrder = inOrder && v.InOrderDelivery
			}

			// receiverLatency <- total / receivedCount
			completeChan <- MsgResult{
				Latency:         time.Duration(int(total/time.Millisecond)/len(payloads)) * time.Millisecond,
				InOrderDelivery: inOrder,
			}
		}

	}()

	for _, payload := range payloads {
		ctx := context.Background()

		// Create a different message to send asynchronously
		asyncMsg := pulsar.ProducerMessage{
			Payload: payload,
		}

		sentTime := time.Now()
		sentPayloads[string(payload)] = &MsgResult{SentTime: sentTime}
		// Attempt to send the message asynchronously and handle the response
		producer.SendAsync(ctx, &asyncMsg, func(messageId pulsar.MessageID, msg *pulsar.ProducerMessage, err error) {
			if err != nil {
				errMsg := fmt.Sprintf("fail to instantiate Pulsar client: %v", err)
				log.Println(errMsg)
				// report error and exit
				errorChan <- errors.New(errMsg)
			}

			log.Println("successfully published ", sentTime)
		})
	}

	select {
	case receiverLatency := <-completeChan:
		return receiverLatency, nil
	case reportedErr := <-errorChan:
		return MsgResult{Latency: failedLatency}, reportedErr
	case <-time.Tick(time.Duration(5*len(payloads)) * time.Second):
		return MsgResult{Latency: failedLatency}, errors.New("latency measure not received after timeout")
	}
}

// MeasureLatency measures pub sub latency of each cluster
func MeasureLatency() {
	token := AssignString(GetConfig().PulsarPerfConfig.Token, GetConfig().PulsarOpsConfig.MasterToken)
	for _, cluster := range GetConfig().PulsarPerfConfig.TopicCfgs {
		expectedLatency := TimeDuration(cluster.LatencyBudgetMs, latencyBudget, time.Millisecond)
		prefix := "messageid"
		payloads := AllMsgPayloads(prefix, cluster.PayloadSizes, cluster.NumOfMessages)
		log.Printf("send %d messages to topic %s on cluster %s with latency budget %v, %v, %d\n",
			len(payloads), cluster.TopicName, cluster.PulsarURL, expectedLatency, cluster.PayloadSizes, cluster.NumOfMessages)
		result, err := PubSubLatency(token, cluster.PulsarURL, cluster.TopicName, prefix, payloads)

		// uri is in the form of pulsar+ssl://useast1.gcp.kafkaesque.io:6651
		clusterName := getNames(cluster.PulsarURL)
		log.Printf("cluster %s has message latency %v", clusterName, result.Latency)
		if err != nil {
			errMsg := fmt.Sprintf("cluster %s latency test Pulsar error: %v", clusterName, err)
			Alert(errMsg)
			ReportIncident(clusterName, "persisted latency test failure", errMsg, &cluster.AlertPolicy)
		} else if !result.InOrderDelivery {
			errMsg := fmt.Sprintf("cluster %s Pulsar message received out of order", clusterName)
			Alert(errMsg)
		} else if result.Latency > expectedLatency {
			errMsg := fmt.Sprintf("cluster %s message latency %v over the budget %v",
				clusterName, result.Latency, expectedLatency)
			Alert(errMsg)
			ReportIncident(clusterName, "persisted latency test failure", errMsg, &cluster.AlertPolicy)
		} else {
			log.Printf("send %d messages to topic %s on cluster %s succeeded\n",
				len(payloads), cluster.TopicName, cluster.PulsarURL)
			ClearIncident(clusterName)
		}
		PromLatencySum(MsgLatencyGaugeOpt(), clusterName, result.Latency)
	}
}

// getNames in the format for reporting and Prometheus metrics
// Input URL pulsar+ssl://useast1.gcp.kafkaesque.io:6651
func getNames(url string) string {
	name := strings.Split(Trim(url), ":")[1]
	clusterName := strings.Replace(name, "//", "", -1)
	return clusterName
}
