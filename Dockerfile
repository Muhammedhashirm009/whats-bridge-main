FROM golang:1.26-alpine AS builder

WORKDIR /app

# Install git (needed for some Go dependencies)
RUN apk add --no-cache git

# Download dependencies first (cache layer)
COPY go.mod go.sum ./
RUN go mod download

# Copy source code and build
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o whatsbridge main.go

# ---- Runtime Stage ----
FROM alpine:3.21

# Install ca-certificates for TLS connections to external services
RUN apk add --no-cache ca-certificates tzdata

WORKDIR /app

# Copy binary and static files
COPY --from=builder /app/whatsbridge .
COPY --from=builder /app/public ./public

EXPOSE 8000

ENV PORT=8000

CMD ["./whatsbridge"]
