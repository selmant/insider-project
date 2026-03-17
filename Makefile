.PHONY: build run test test-integration lint docker-up docker-down clean

build:
	go build -o bin/insider ./cmd/insider

run:
	go run ./cmd/insider

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

migrate:
	go run ./cmd/insider migrate
