# Build stage
FROM golang:1.24-alpine AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /hello ./cmd/hello

# Runtime stage
FROM alpine:3.19

COPY --from=builder /hello /usr/local/bin/hello
COPY antithesis/setup-complete.sh /usr/local/bin/setup-complete.sh
RUN chmod +x /usr/local/bin/setup-complete.sh

EXPOSE 8080

CMD ["/usr/local/bin/hello"]
