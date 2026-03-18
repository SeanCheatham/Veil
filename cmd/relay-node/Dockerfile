FROM golang:1.26-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /relay-node ./cmd/relay-node

FROM alpine:latest
RUN apk add --no-cache wget
COPY --from=builder /relay-node /relay-node
EXPOSE 8080
CMD ["/relay-node"]
