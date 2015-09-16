package client

import (
	"errors"
	"fmt"
	"sync"

	"h12.me/kafka/broker"
	"h12.me/kafka/proto"
)

var (
	ErrTopicNotFound  = errors.New("topic not found")
	ErrLeaderNotFound = errors.New("leader not found")
	ErrCoordNotFound  = errors.New("coordinator not found")
	ErrNoBrokerFound  = errors.New("no broker found")
)

type Config struct {
	Brokers      []string
	BrokerConfig broker.Config
	ClientID     string
}

type C struct {
	brokers              map[int32]*broker.B
	topicPartitions      map[string][]int32
	topicPartitionLeader map[topicPartition]*broker.B
	groupCoordinator     map[string]*broker.B
	config               *Config
	mu                   sync.Mutex
}

type topicPartition struct {
	topic     string
	partition int32
}

func New(config *Config) (*C, error) {
	c := &C{
		brokers:              make(map[int32]*broker.B),
		config:               config,
		topicPartitions:      make(map[string][]int32),
		topicPartitionLeader: make(map[topicPartition]*broker.B),
		groupCoordinator:     make(map[string]*broker.B),
	}
	return c, nil
}

func (c *C) Partitions(topic string) ([]int32, error) {
	if partitions, ok := c.topicPartitions[topic]; ok {
		return partitions, nil
	}
	if err := c.updateFromTopicMetadata(topic); err != nil {
		return nil, err
	}
	if partitions, ok := c.topicPartitions[topic]; ok {
		return partitions, nil
	}
	return nil, ErrTopicNotFound
}

func (c *C) Coordinator(topic, consumerGroup string) (*broker.B, error) {
	if coord, ok := c.groupCoordinator[consumerGroup]; ok {
		return coord, nil
	}
	if err := c.updateFromConsumerMetadata(topic, consumerGroup); err != nil {
		return nil, err
	}
	if coord, ok := c.groupCoordinator[consumerGroup]; ok {
		return coord, nil
	}
	return nil, ErrCoordNotFound
}

func (c *C) Leader(topic string, partition int32) (*broker.B, error) {
	key := topicPartition{topic, partition}
	if leader, ok := c.topicPartitionLeader[key]; ok {
		return leader, nil
	}
	if err := c.updateFromTopicMetadata(topic); err != nil {
		return nil, err
	}
	if leader, ok := c.topicPartitionLeader[key]; ok {
		return leader, nil
	}
	return nil, ErrLeaderNotFound
}

func (c *C) updateFromConsumerMetadata(topic, consumerGroup string) error {
	m, err := c.getConsumerMetadata(consumerGroup)
	if err != nil {
		return err
	}
	if broker, ok := c.brokers[m.CoordinatorID]; ok {
		c.groupCoordinator[consumerGroup] = broker
		return nil
	}
	if err := c.updateFromTopicMetadata(topic); err != nil {
		return err
	}
	if broker, ok := c.brokers[m.CoordinatorID]; ok {
		c.groupCoordinator[consumerGroup] = broker
		return nil
	}
	return ErrCoordNotFound
}

func (c *C) updateFromTopicMetadata(topic string) error {
	m, err := c.getTopicMetadata(topic)
	if err != nil {
		return err
	}
	for i := range m.Brokers {
		b := &m.Brokers[i]
		if _, ok := c.brokers[b.NodeID]; !ok {
			cfg := c.config.BrokerConfig
			cfg.Addr = fmt.Sprintf("%s:%d", b.Host, b.Port)
			broker, err := broker.New(&cfg)
			if err == nil {
				c.brokers[b.NodeID] = broker
			} else {
				// TODO: log
			}
		}
	}
	for i := range m.TopicMetadatas {
		t := &m.TopicMetadatas[i]
		if t.TopicName == topic {
			partitions := make([]int32, len(t.PartitionMetadatas))
			for i := range t.PartitionMetadatas {
				partition := t.PartitionMetadatas[i].PartitionID
				partitions[i] = partition
				if broker, ok := c.brokers[t.PartitionMetadatas[i].Leader]; ok {
					c.topicPartitionLeader[topicPartition{topic, partition}] = broker
				}
			}
			c.topicPartitions[topic] = partitions
			return nil
		}
	}
	return ErrTopicNotFound
}

func (c *C) getAnyBroker() (broker *broker.B, needClosing bool, err error) {
	for _, b := range c.brokers {
		return b, false, nil
	}
	broker, err = c.getBootstrapBroker()
	if err != nil {
		return nil, false, err
	}
	return broker, true, nil
}

func (c *C) getTopicMetadata(topic string) (*proto.TopicMetadataResponse, error) {
	broker, needClosing, err := c.getAnyBroker()
	if err != nil {
		return nil, err
	}
	if needClosing {
		defer broker.Close()
	}
	req := &proto.Request{
		APIKey:         proto.TopicMetadataRequestType,
		APIVersion:     0,
		ClientID:       c.config.ClientID,
		RequestMessage: &proto.TopicMetadataRequest{topic},
	}
	resp := &proto.TopicMetadataResponse{}
	if err := broker.Do(req, &proto.Response{ResponseMessage: resp}); err != nil {
		return nil, err
	}
	return resp, nil
}

func (c *C) getConsumerMetadata(consumerGroup string) (*proto.ConsumerMetadataResponse, error) {
	broker, needClosing, err := c.getAnyBroker()
	if err != nil {
		return nil, err
	}
	if needClosing {
		defer broker.Close()
	}
	creq := proto.ConsumerMetadataRequest(consumerGroup)
	req := &proto.Request{
		APIKey:         proto.ConsumerMetadataRequestType,
		APIVersion:     0,
		ClientID:       c.config.ClientID,
		RequestMessage: &creq,
	}
	resp := proto.ConsumerMetadataResponse{}
	if err := broker.Do(req, &proto.Response{ResponseMessage: &resp}); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *C) getBootstrapBroker() (*broker.B, error) {
	for _, addr := range c.config.Brokers {
		cfg := c.config.BrokerConfig
		cfg.Addr = addr
		broker, err := broker.New(&cfg)
		if err == nil {
			return broker, nil
		}
	}
	return nil, ErrNoBrokerFound
}
