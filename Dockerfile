# ----------------------------------
# Pterodactyl Panel Dockerfile
# ----------------------------------

FROM golang:1.14-alpine
COPY . /go/wings/
WORKDIR /go/wings/
RUN apk add --no-cache upx \
 && go build -ldflags="-s -w" \
 && upx --brute wings

FROM alpine:latest
COPY --from=0 /go/wings/wings /usr/bin/
COPY ./templates /templates
CMD ["wings","--config", "/var/lib/pterodactyl/config.yml"]
