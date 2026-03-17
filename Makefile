.PHONY: build build-api build-workers test test-integration lint docker-up docker-down clean

build: build-api build-workers

build-api:
	go build -o bin/api ./cmd/api

build-workers:
	go build -o bin/worker-sms ./cmd/worker-sms
	go build -o bin/worker-email ./cmd/worker-email
	go build -o bin/worker-push ./cmd/worker-push

run-api:
	go run ./cmd/api

run-worker-sms:
	go run ./cmd/worker-sms

run-worker-email:
	go run ./cmd/worker-email

run-worker-push:
	go run ./cmd/worker-push

test:
	go test ./... -short -race -count=1

test-integration:
	go test ./... -tags=integration -race -count=1

lint:
	golangci-lint run

docker-up:
	docker compose -f deployments/docker-compose.yaml up --build -d

docker-down:
	docker compose -f deployments/docker-compose.yaml down -v

clean:
	rm -rf bin/
