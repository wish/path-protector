FROM       golang:alpine as builder

COPY . /go/src/github.com/wish/path-protector
RUN cd /go/src/github.com/wish/path-protector && CGO_ENABLED=0 go build

FROM alpine:latest

COPY --from=builder /go/src/github.com/wish/path-protector/path-protector /bin/path-protector
