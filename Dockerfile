# ----------------------------------
# Pterodactyl Panel Dockerfile
# ----------------------------------

FROM golang:1.15-alpine
COPY . /go/wings/
WORKDIR /go/wings/
RUN apk add --no-cache upx \
    && CGO_ENABLED=0 go build -ldflags="-s -w" \
    && upx --brute wings

FROM alpine:latest
COPY --from=0 /go/wings/wings /usr/bin/
CMD ["wings","--config", "/etc/pterodactyl/config.yml"]