BINARY = "build/wings"

all: $(BINARY)

$(BINARY):
	go build -o $(BINARY)

cross-build:
	gox -output "build/{{.Dir}}_{{.OS}}_{{.Arch}}"

.PHONY: install
install:
	go install

test:
	go test `go list ./... | grep -v "/vendor/"`

coverage:
	goverage -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out

dependencies:
	dep ensure

install-tools:
	go get -u github.com/golang/dep/cmd/dep
	go get -u github.com/mitchellh/gox
	go get -u github.com/haya14busa/goverage
