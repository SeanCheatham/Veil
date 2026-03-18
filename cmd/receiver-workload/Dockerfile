FROM golang:1.26-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /receiver-workload ./cmd/receiver-workload
RUN CGO_ENABLED=0 go build -o /parallel_driver_verify_messages ./antithesis/test/veil/parallel_driver_verify_messages.go
RUN CGO_ENABLED=0 go build -o /finally_delivery_check ./antithesis/test/veil/finally_delivery_check.go
RUN CGO_ENABLED=0 go build -o /anytime_no_spurious_messages ./antithesis/test/veil/anytime_no_spurious_messages.go
RUN CGO_ENABLED=0 go build -o /anytime_cover_traffic ./antithesis/test/veil/anytime_cover_traffic.go

FROM alpine:latest
RUN apk add --no-cache wget
COPY --from=builder /receiver-workload /receiver-workload
RUN mkdir -p /opt/antithesis/test/v1/veil
COPY --from=builder /parallel_driver_verify_messages /opt/antithesis/test/v1/veil/
COPY --from=builder /finally_delivery_check /opt/antithesis/test/v1/veil/
COPY --from=builder /anytime_no_spurious_messages /opt/antithesis/test/v1/veil/
COPY --from=builder /anytime_cover_traffic /opt/antithesis/test/v1/veil/
CMD ["/receiver-workload"]
