FROM golang:1.26-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /validator-node ./cmd/validator-node

FROM alpine:latest
RUN apk add --no-cache wget
COPY --from=builder /validator-node /validator-node
EXPOSE 8081
CMD ["/validator-node"]
