build:
	go build checker.go

lint:
	go fmt ./...
	go vet ./...
	golint ./pkg...
	golint ./checker.go

run:
	$(MAKE) build
	./checker -a localhost:5299 --ops 10000

