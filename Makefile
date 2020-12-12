build:
	GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -gcflags "all=-trimpath=$(pwd)" -o build/wings_linux_amd64 -v wings.go
	GOOS=linux GOARCH=arm64 go build -ldflags="-s -w" -gcflags "all=-trimpath=$(pwd)" -o build/wings_linux_arm64 -v wings.go

debug:
	go build -race
	./wings --debug --ignore-certificate-errors --config config.yml

compress:
	upx --brute build/wings_*

cross-build: clean build compress

clean:
	rm -rf build/wings_*

.PHONY: all build compress clean