BINARY = "build/wings"
OSARCHLIST = "darwin/386 darwin/amd64 linux/386 linux/amd64 linux/arm linux/arm64 windows/386 windows/amd64"

all: $(BINARY)

$(BINARY):
	go build -o $(BINARY)

cross-build:
	gox -osarch $(OSARCHLIST) -output "build/{{.Dir}}_{{.OS}}_{{.Arch}}"

.PHONY: install
install:
	go install

test:
	go test `go list ./... | grep -v "/vendor/"`

coverage:
	goverage -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out

dependencies:
	glide install

install-tools:
	go get -u github.com/mitchellh/gox
	go get -u github.com/haya14busa/goverage
