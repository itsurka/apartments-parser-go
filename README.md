# Apartments pet project (data parser on Golang)
Contains following docker services:
- Web parser app on Go
- PostgreSQL for storing apartments
- RabbitMQ with 2 queues
  - apartments.pending // run parser events
  - apartments.done // parser completed events

## Install
Make env file from example
```shell
cp .env.example .env
```

And run docker compose
```shell
docker-compose up -d --biuld
```

Service is waiting for messages on RabbitMQ queue
```shell
apartments.pending
```

Then it run parsing an apartments data and writes message to the RabbitMQ queue
```shell
apartments.done
```
