FROM golang:latest

RUN mkdir /backend
WORKDIR /backend
ADD ./backend /backend

RUN go install -mod=mod github.com/githubnemo/CompileDaemon

ENTRYPOINT CompileDaemon --build="go build" --command=./backend