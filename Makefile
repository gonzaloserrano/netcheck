.PHONY: build run clean fmt vet lint test

BINARY := netcheck

build:
	go build -o $(BINARY) .

run: build
	./$(BINARY)

clean:
	rm -f $(BINARY)

fmt:
	go fmt ./...

vet:
	go vet ./...

lint: fmt vet

test:
	go test -v ./...

tidy:
	go mod tidy

deps:
	go get -u ./...
	go mod tidy
