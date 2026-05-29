# Stage 1: compile all binaries
FROM golang:1.26-alpine AS builder
WORKDIR /app

# Download deps first (cached unless go.mod/go.sum change)
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN go build -o /app/bin/scheduler  ./cmd/scheduler  && \
    go build -o /app/bin/discovery  ./cmd/discovery  && \
    go build -o /app/bin/fetcher    ./cmd/fetcher    && \
    go build -o /app/bin/parser     ./cmd/parser     && \
    go build -o /app/bin/enricher   ./cmd/enricher   && \
    go build -o /app/bin/api        ./cmd/api        && \
    go build -o /app/bin/topology   ./cmd/topology

# Install goose for migrations
RUN go install github.com/pressly/goose/v3/cmd/goose@latest

# Stage 2: minimal runtime image
FROM alpine:3.20
RUN apk --no-cache add ca-certificates tzdata

COPY --from=builder /app/bin/        /usr/local/bin/
COPY --from=builder /go/bin/goose    /usr/local/bin/goose
COPY migrations/                     /app/migrations/