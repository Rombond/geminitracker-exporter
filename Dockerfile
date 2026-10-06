FROM golang:alpine AS builder

WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the binary
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o geminitracker_exporter .

FROM alpine:latest

RUN adduser -D -u 1000 exporter

# Add ca-certificates for HTTPS requests and wget for the health check
RUN apk --no-cache add ca-certificates tzdata wget

# Copy binary to a known, stable path
COPY --from=builder /app/geminitracker_exporter /usr/local/bin/geminitracker_exporter

USER exporter

EXPOSE 9090

HEALTHCHECK --interval=30s --timeout=5s --start-period=40s --retries=3 \
    CMD wget -qO- http://localhost:9090/health || exit 1

ENTRYPOINT ["/usr/local/bin/geminitracker_exporter"]
