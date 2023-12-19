FROM golang:1.20 as builder
ENV PATH /go/bin:/usr/local/go/bin:$PATH
ENV GOPATH /go
COPY src/ /go/src/
WORKDIR /go/src/
RUN go mod download
RUN go build .

FROM ubuntu:latest
RUN apt-get update
COPY --from=builder /go/src/kubelet-cloud-run /usr/bin/kubelet-cloud-run
COPY --from=builder /etc/ssl/certs/ /etc/ssl/certs
COPY src/vkubelet-cfg.json /vkubelet-cfg.json
ENTRYPOINT [ "/usr/bin/kubelet-cloud-run" ]
CMD [ "--help" ]
