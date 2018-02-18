# workspace (GOPATH) configured at /go.
FROM golang:1.9.4-alpine3.6

# Copy the local package files to the container's workspace.
#ADD ./server.json /go/bin

RUN apk add --update git && \
    go get -d github.com/devcodewak/avonsg_openshift  && \
    go build -v -ldflags="-s -w" -o /go/bin/web github.com/devcodewak/avonsg_openshift/cmd  && \
    rm -rf /go/src/github.com/devcodewak/

#WORKDIR /go/bin
# Run the outyet command by default when the container starts.
CMD ["/go/bin/web", "-server", "-cmd", "-key", "809240d3a021449f6e67aa73221d42df942a308a", "-http2", ":8443", "-http", ":8444", "-log", "console"]
# Document that the service listens on port 8443.
EXPOSE 8443 8444