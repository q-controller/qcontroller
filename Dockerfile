FROM golang:1.24.6-bookworm AS pre-build

ARG GROUP_ID
ARG USER_ID

RUN apt update
RUN apt install -y protobuf-compiler
RUN apt install -y default-jre

RUN if ! getent group ${GROUP_ID} > /dev/null; then \
        addgroup --gid ${GROUP_ID} qcontrollerd; \
    fi && \
    adduser --uid ${USER_ID} --gid ${GROUP_ID} --disabled-password --gecos "" qcontrollerd


RUN apt install -y python3-venv
COPY prepare.sh /usr/local/bin/prepare.sh
RUN chmod +x /usr/local/bin/prepare.sh

USER qcontrollerd

RUN /usr/local/bin/prepare.sh
