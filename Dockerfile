FROM alpine:latest

RUN mkdir /etc/egeon /egeon
RUN apk add --no-cache tzdata curl
COPY egeonEmail /egeon/
COPY conf.yaml /etc/egeon/
COPY ca-crt.pem /etc/egeon/
COPY egeonemail-crt.pem /etc/egeon/
COPY egeonemail-key.pem /etc/egeon/

WORKDIR /egeon

CMD ["./egeonEmail", "/etc/egeon/conf.yaml"]