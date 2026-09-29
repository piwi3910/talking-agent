FROM alpine:3.22
RUN apk add --no-cache openssh-client python3 ca-certificates && adduser -D -u 10001 deployer
USER 10001
ENTRYPOINT ["python3", "/release/dispatch.py"]
