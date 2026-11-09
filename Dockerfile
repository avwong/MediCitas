# One Dockerfile for every service: SERVICE selects the binary in cmd/.
FROM golang:1.27-alpine AS builder

ARG SERVICE

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN test -n "$SERVICE" || (echo "SERVICE build argument is required" && exit 1)

RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w" \
    -o /out/service \
    "./cmd/${SERVICE}"


FROM alpine:3.22

RUN addgroup -S app && adduser -S -G app app

WORKDIR /app

COPY --from=builder /out/service /app/service

USER app

EXPOSE 8080

ENTRYPOINT ["/app/service"]
