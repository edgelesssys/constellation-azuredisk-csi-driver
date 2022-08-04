# Copyright Edgeless Systems GmbH
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

FROM debian:bullseye AS build

ENV DEBIAN_FRONTEND="noninteractive"
RUN apt-get update && apt-get install -y build-essential wget pkg-config libcryptsetup12 libcryptsetup-dev
ARG GO_VER=1.18.2
RUN wget https://go.dev/dl/go${GO_VER}.linux-amd64.tar.gz && \
    tar -C /usr/local -xzf go${GO_VER}.linux-amd64.tar.gz && \
    rm go${GO_VER}.linux-amd64.tar.gz
ENV PATH ${PATH}:/usr/local/go/bin

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
