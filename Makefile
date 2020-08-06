build:
	GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -gcflags "all=-trimpath=$(PWD)" -o build/wings_linux_amd64 -v wings.go

compress:
	upx --brute build/wings_*

cross-build: clean build compress

clean:
	rm -rf build/wings_*

.PHONY: all build compress clean