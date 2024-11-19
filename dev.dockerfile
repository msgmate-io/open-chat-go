FROM golang:latest

RUN mkdir /backend
RUN mkdir /dev_bin
WORKDIR /backend
ADD ./backend /backend

RUN GOBIN="/dev_bin" go install -mod=mod github.com/swaggo/swag/v2/cmd/swag@latest
RUN GOBIN="/dev_bin" go install -mod=mod github.com/githubnemo/CompileDaemon

ENTRYPOINT /dev_bin/CompileDaemon --build="/dev_bin/swag init && go build" --command=./backend --exclude-dir=docs