FROM golang:1.27-alpine AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w" \
    -o /out/medicitas \
    ./cmd/api


FROM alpine:3.22

RUN addgroup -S app && adduser -S -G app app

WORKDIR /app

COPY --from=builder /out/medicitas /app/medicitas

USER app

EXPOSE 8080

ENTRYPOINT ["/app/medicitas"]