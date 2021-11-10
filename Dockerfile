FROM alpine:latest

RUN mkdir /etc/egeon /egeon
RUN apk add --no-cache tzdata curl
COPY email /egeon/
COPY conf.yaml /etc/egeon/
WORKDIR /egeon

CMD ["./email", "/etc/egeon/conf.yaml"]