FROM golang:1.14 AS build

ENV GOPATH=/go
WORKDIR /app
COPY . .

RUN go get -d \
    && go build -o pod-lifecycle *.go

FROM golang:1.14

COPY --from=build /app/pod-lifecycle /usr/bin/pod-lifecycle

ENTRYPOINT ["/usr/bin/pod-lifecycle"]
