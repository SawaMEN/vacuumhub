FROM golang:1.23-bookworm AS build
WORKDIR /src
RUN apt-get update && apt-get install -y --no-install-recommends libsqlite3-dev gcc && rm -rf /var/lib/apt/lists/*
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=1 go build -trimpath -ldflags='-s -w' -o /vacuumhub ./cmd/vacuumhub

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates libsqlite3-0 && rm -rf /var/lib/apt/lists/* \
    && groupadd -g 10001 app && useradd --no-create-home -u 10001 -g app app && mkdir -p /data /app && chown -R app:app /data
WORKDIR /app
COPY --from=build /vacuumhub /usr/local/bin/vacuumhub
COPY web ./web
USER app
ENV DATA_DIR=/data WEB_DIR=/app/web LISTEN_ADDR=:8080
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=4s --start-period=20s --retries=3 CMD ["/usr/local/bin/vacuumhub", "--healthcheck"]
ENTRYPOINT ["/usr/local/bin/vacuumhub"]
