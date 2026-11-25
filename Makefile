.PHONY: all test
all:
	go build -o tmwell ./cmd/tmwell

test:
	go vet ./...
	go test ./...

MAN_NAME = tmwell
include build/man.mk
