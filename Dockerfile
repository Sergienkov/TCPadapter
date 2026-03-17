FROM golang:1.22-alpine AS builder

WORKDIR /src

ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_TIME=unknown

COPY go.mod ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags="-X tcpadapter/internal/buildinfo.Version=${VERSION} -X tcpadapter/internal/buildinfo.Commit=${COMMIT} -X tcpadapter/internal/buildinfo.BuildTime=${BUILD_TIME}" \
    -o /out/tcpadapter ./cmd/tcpadapter && \
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags="-X tcpadapter/internal/buildinfo.Version=${VERSION} -X tcpadapter/internal/buildinfo.Commit=${COMMIT} -X tcpadapter/internal/buildinfo.BuildTime=${BUILD_TIME}" \
    -o /out/controller-sim ./cmd/controller-sim

FROM alpine:3.20

RUN addgroup -S app && adduser -S -G app app && \
    mkdir -p /data && chown -R app:app /data

COPY --from=builder /out/tcpadapter /usr/local/bin/tcpadapter
COPY --from=builder /out/controller-sim /usr/local/bin/controller-sim

USER app
WORKDIR /app

EXPOSE 15010 18080

ENTRYPOINT ["/usr/local/bin/tcpadapter"]
