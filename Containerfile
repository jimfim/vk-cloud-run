FROM golang:1.20 as builder
ENV PATH /go/bin:/usr/local/go/bin:$PATH
ENV GOPATH /go
COPY src/ /go/src/
WORKDIR /go/src/
RUN go mod download
RUN go build .

FROM scratch
COPY --from=builder /go/src/virtual-kubelet /usr/bin/virtual-kubelet
COPY --from=builder /etc/ssl/certs/ /etc/ssl/certs
#COPY hack/skaffold/virtual-kubelet/vkubelet-mock-0-cfg.json /vkubelet-mock-0-cfg.json
ENTRYPOINT [ "/usr/bin/virtual-kubelet" ]
CMD [ "--help" ]
