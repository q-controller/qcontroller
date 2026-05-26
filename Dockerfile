FROM golang:1.25.10-bookworm AS pre-build

ARG GROUP_ID
ARG USER_ID
ARG NODE_OPTIONS=

RUN apt update
RUN apt install -y protobuf-compiler
RUN apt install -y default-jre
# build-essential provides gcc / libc6-dev required by cgo (mattn/go-sqlite3
# pulled in via coredhcp's range plugin). Without it `go build` silently
# defaults to CGO_ENABLED=0 and the binary stubs out sqlite at runtime.
RUN apt install -y build-essential

RUN if ! getent group ${GROUP_ID} > /dev/null; then \
        addgroup --gid ${GROUP_ID} qcontrollerd; \
    fi && \
    adduser --uid ${USER_ID} --gid ${GROUP_ID} --disabled-password --gecos "" qcontrollerd


RUN apt install -y python3-venv

# Stage prepare.sh + schema/prepare.sh under a single root so the wrapper's
# ${script_dir}/schema/prepare.sh resolution works.
COPY prepare.sh /usr/local/qcontroller-bootstrap/prepare.sh
COPY schema/prepare.sh /usr/local/qcontroller-bootstrap/schema/prepare.sh
RUN chmod +x /usr/local/qcontroller-bootstrap/prepare.sh \
             /usr/local/qcontroller-bootstrap/schema/prepare.sh \
    && chown -R ${USER_ID}:${GROUP_ID} /usr/local/qcontroller-bootstrap

USER qcontrollerd

RUN NODE_OPTIONS="${NODE_OPTIONS}" /usr/local/qcontroller-bootstrap/prepare.sh
ENTRYPOINT ["/bin/bash", "-i", "-c"]
