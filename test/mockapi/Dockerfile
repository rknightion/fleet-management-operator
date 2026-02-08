# Build stage
FROM golang:1.25 AS builder

WORKDIR /workspace

# Copy source
COPY . .

# Build binary
RUN CGO_ENABLED=0 GOOS=linux go build -a -o mockapi .

# Runtime stage
FROM gcr.io/distroless/static:nonroot

WORKDIR /

# Copy binary from builder
COPY --from=builder /workspace/mockapi .

USER 65532:65532

EXPOSE 8080

ENTRYPOINT ["/mockapi"]
