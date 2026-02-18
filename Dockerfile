FROM golang:1.22-alpine AS builder

WORKDIR /src

COPY go.mod ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/tcpadapter ./cmd/tcpadapter && \
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/controller-sim ./cmd/controller-sim

FROM alpine:3.20

RUN addgroup -S app && adduser -S -G app app && \
    mkdir -p /data && chown -R app:app /data

COPY --from=builder /out/tcpadapter /usr/local/bin/tcpadapter
COPY --from=builder /out/controller-sim /usr/local/bin/controller-sim

USER app
WORKDIR /app

EXPOSE 15010 18080

ENTRYPOINT ["/usr/local/bin/tcpadapter"]
