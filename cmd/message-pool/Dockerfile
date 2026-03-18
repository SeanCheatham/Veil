FROM golang:1.26-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /message-pool ./cmd/message-pool

FROM alpine:latest
RUN apk add --no-cache wget
COPY --from=builder /message-pool /message-pool
EXPOSE 8082
CMD ["/message-pool"]
