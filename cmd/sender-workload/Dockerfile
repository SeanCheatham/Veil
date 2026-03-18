FROM golang:1.26-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /sender-workload ./cmd/sender-workload
RUN CGO_ENABLED=0 go build -o /parallel_driver_send_message ./antithesis/test/veil/parallel_driver_send_message.go
RUN CGO_ENABLED=0 go build -o /first_setup_check ./antithesis/test/veil/first_setup_check.go

FROM alpine:latest
RUN apk add --no-cache wget
COPY --from=builder /sender-workload /sender-workload
RUN mkdir -p /opt/antithesis/test/v1/veil
COPY --from=builder /parallel_driver_send_message /opt/antithesis/test/v1/veil/
COPY --from=builder /first_setup_check /opt/antithesis/test/v1/veil/
CMD ["/sender-workload"]
