FROM golang:1.15.10

WORKDIR /scheduler-build
COPY scheduler/go.mod go.mod
COPY scheduler/go.sum go.sum
# cache deps before building and copying source so that we don't need to re-download as much
# and so that source changes don't invalidate our downloaded layer
RUN go mod download

COPY scheduler/undermoon_scheduler.go undermoon_scheduler.go
COPY scheduler/pkg/ pkg/

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GO111MODULE=on go build -a -o undermoon-scheduler undermoon_scheduler.go

FROM alpine:3.12

COPY --from=0 /scheduler-build/undermoon-scheduler /bin/undermoon-scheduler

WORKDIR /bin
CMD ["undermoon-scheduler"]