FROM golang:1.22-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /bin/api ./cmd/api && \
    CGO_ENABLED=0 GOOS=linux go build -o /bin/worker-sms ./cmd/worker-sms && \
    CGO_ENABLED=0 GOOS=linux go build -o /bin/worker-email ./cmd/worker-email && \
    CGO_ENABLED=0 GOOS=linux go build -o /bin/worker-push ./cmd/worker-push

FROM alpine:3.19

RUN apk --no-cache add ca-certificates tzdata
WORKDIR /app

COPY --from=builder /bin/api .
COPY --from=builder /bin/worker-sms .
COPY --from=builder /bin/worker-email .
COPY --from=builder /bin/worker-push .

EXPOSE 8080
