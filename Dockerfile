FROM golang:1.14 AS build

ENV GOPATH=/go
WORKDIR /app
COPY . .

RUN go get -d \
    && go build -o deploment-reaper *.go

FROM golang:1.14

COPY --from=build /app/deploment-reaper /usr/bin/deploment-reaper

# Run as daemon user, use of root is discouraged
USER 2

ENTRYPOINT ["/usr/bin/deploment-reaper"]
