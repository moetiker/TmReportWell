.PHONY: all test
all:
	go build -o tmreportwell ./cmd/tmreportwell

test:
	go vet ./...
	go test ./...

MAN_NAME = tmreportwell
include build/man.mk
