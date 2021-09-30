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

localtest:
	./checker -a localhost:6001 -m localhost:7001 -m localhost:7002 --ops 1000 --monitor-buf 100000

