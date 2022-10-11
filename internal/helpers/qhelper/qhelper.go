package qhelper

import (
	json2 "encoding/json"
	_ "github.com/lib/pq"
	"github.com/streadway/amqp"
	"itsurka/go-web-parser/internal/dto"
	eh "itsurka/go-web-parser/internal/helpers/errhelper"
)

type Queue struct {
	ConnString string
	connection *amqp.Connection
	channel    *amqp.Channel
}

func (q *Queue) Publish(queueName string, message dto.QueueMessage) {
	q.init()
	amqpQueue := q.getQueue(queueName)

	body, err := json2.Marshal(message)
	eh.FailOnError(err)

	publishErr := q.channel.Publish(
		"",
		amqpQueue.Name,
		false,
		false,
		amqp.Publishing{
			ContentType: "application/json",
			Body:        body,
		},
	)
	eh.PanicOnError(publishErr)
}

func (q *Queue) Consume(queueName string) <-chan amqp.Delivery {
	q.init()
	amqpQueue := q.getQueue(queueName)

	messages, err := q.channel.Consume(
		amqpQueue.Name,
		"",
		true,
		false,
		false,
		false,
		nil,
	)
	eh.FailOnError(err)

	return messages
}

func (q *Queue) getQueue(queueName string) amqp.Queue {
	amqpQueue, err := q.channel.QueueDeclare(
		queueName,
		true,
		false,
		false,
		false,
		nil,
	)
	eh.FailOnError(err)

	return amqpQueue
}

func (q *Queue) init() {
	q.initConnection()
	q.initChannel()
}

func (q *Queue) initChannel() {
	if q.channel != nil {
		return
	}

	ch, err := q.connection.Channel()
	eh.FailOnError(err)

	q.channel = ch
}

func (q *Queue) initConnection() {
	if q.connection != nil {
		return
	}

	conn, err := amqp.Dial(q.ConnString)
	eh.FailOnError(err)

	q.connection = conn
}
