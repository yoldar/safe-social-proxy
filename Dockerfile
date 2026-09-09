FROM golang:1.23-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/proxy ./cmd/proxy

# Debian (glibc): the YouTube frontend shells out to yt-dlp, which needs a JS
# runtime (Deno) to solve YouTube's nsig challenge — without it extraction
# fails with "The page needs to be reloaded". Deno's official binary is glibc.
FROM debian:12-slim
RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        ca-certificates python3 python3-pip curl unzip \
    && pip install --no-cache-dir --break-system-packages yt-dlp bgutil-ytdlp-pot-provider \
    && curl -fsSL https://deno.land/install.sh | DENO_INSTALL=/usr/local sh -s -- -y \
    && apt-get purge -y curl unzip \
    && apt-get autoremove -y \
    && rm -rf /var/lib/apt/lists/*
# debian already ships a system user "proxy" (uid 13); reuse it (see USER below).
WORKDIR /app
COPY --from=build /out/proxy /app/proxy
COPY config.yaml /app/config.yaml
# Deno (run by yt-dlp) and other tools need a writable home/cache; the proxy
# user has none, so point them at /tmp.
ENV HOME=/tmp DENO_DIR=/tmp/deno
USER proxy
EXPOSE 8080
ENTRYPOINT ["/app/proxy", "-config", "/app/config.yaml"]
