FROM golang:1.15-alpine
ARG VERSION="develop"
COPY . /go/wings/
WORKDIR /go/wings/
RUN apk add --no-cache upx \
    && CGO_ENABLED=0 go build -ldflags="-s -w -X github.com/pterodactyl/wings/system.Version=${VERSION}" \
    && upx wings

FROM alpine:latest
COPY --from=0 /go/wings/wings /usr/bin/
CMD ["wings", "--config", "/etc/pterodactyl/config.yml"]