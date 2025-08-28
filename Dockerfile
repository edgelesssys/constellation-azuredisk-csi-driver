FROM debian:bookworm AS build

ENV DEBIAN_FRONTEND="noninteractive"
RUN apt-get update && apt-get install -y build-essential git wget pkg-config libcryptsetup12 libcryptsetup-dev
ARG GO_VER=1.24.6
RUN wget https://go.dev/dl/go${GO_VER}.linux-amd64.tar.gz && \
    tar -C /usr/local -xzf go${GO_VER}.linux-amd64.tar.gz && \
    rm go${GO_VER}.linux-amd64.tar.gz
ENV PATH=${PATH}:/usr/local/go/bin

WORKDIR /azurediskplugin
COPY pkg ./pkg
COPY go.mod ./go.mod
COPY go.sum ./go.sum

ARG ARCH
ARG LDFLAGS
ARG PLUGIN_NAME
RUN CGO_ENABLED=1 GOOS=linux GOARCH="${ARCH}" go build -trimpath -a -ldflags "${LDFLAGS}" -o "/azurediskplugin/${PLUGIN_NAME}" ./pkg/azurediskplugin

FROM scratch AS export
ARG PLUGIN_NAME
COPY --from=build /azurediskplugin/${PLUGIN_NAME} /
