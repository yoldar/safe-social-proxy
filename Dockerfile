FROM golang:1.23-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/proxy ./cmd/proxy

# alpine (not distroless): the YouTube frontend shells out to yt-dlp.
FROM alpine:3.22
RUN apk add --no-cache ca-certificates python3 py3-pip \
    && pip install --no-cache-dir --break-system-packages yt-dlp bgutil-ytdlp-pot-provider \
    && adduser -D -H proxy
WORKDIR /app
COPY --from=build /out/proxy /app/proxy
COPY config.yaml /app/config.yaml
USER proxy
EXPOSE 8080
ENTRYPOINT ["/app/proxy", "-config", "/app/config.yaml"]
