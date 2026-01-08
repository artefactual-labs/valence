###
# Static PHP (glibc) build stage for Valence.
# This is intentionally minimal and only compiles the static PHP embed library.
#
# Notes:
# - static-php-cli's glibc build is officially done via its `bin/spc-gnu-docker`
#   wrapper, which uses an older glibc (CentOS 7). We mirror that here by
#   building directly in a CentOS 7 container.
# - This stage produces `buildroot/` artifacts (including libphp.a and spc-config)
#   that are linked into the final Valence binary used in the runtime image.
###

ARG GO_VERSION=1.25.4
ARG NODE_VERSION=22
ARG PHP_VERSION=8.3

# node-build prepares the legacy AtoM frontend assets.
# -----------------------------------------------------------------------------
FROM node:${NODE_VERSION}-bookworm AS node-build

WORKDIR /app/atom
COPY atom/package.json atom/package-lock.json /app/atom/
RUN npm clean-install

COPY atom /app/atom
RUN npm run build

# atom-build prepares the legacy AtoM app (composer deps + baked assets).
# -----------------------------------------------------------------------------
FROM php:${PHP_VERSION}-cli-bookworm AS atom-build
RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        $PHPIZE_DEPS \
        ca-certificates \
        curl \
        git \
        tar \
        unzip \
        libcurl4-openssl-dev \
        libxml2-dev \
    && rm -rf /var/lib/apt/lists/*
RUN docker-php-ext-install curl dom
RUN curl -sS https://getcomposer.org/installer | php -- --install-dir=/usr/local/bin --filename=composer

WORKDIR /app/atom
COPY atom/composer.json atom/composer.lock /app/atom/
RUN composer install --no-dev --prefer-dist --no-interaction --optimize-autoloader
COPY atom /app/atom
COPY --from=node-build /app/atom/css /app/atom/css
COPY --from=node-build /app/atom/dist /app/atom/dist
COPY --from=node-build /app/atom/images /app/atom/images
COPY --from=node-build /app/atom/js /app/atom/js
COPY --from=node-build /app/atom/plugins /app/atom/plugins
COPY --from=node-build /app/atom/web /app/atom/web
RUN mkdir -p /out \
    && tar -C /app/atom -czf /out/atom.tar.gz \
        --exclude=cache \
        --exclude=log \
        --exclude=uploads \
        --exclude=web/uploads \
        --exclude=.git \
        --exclude=.gitmodules \
        .

FROM centos:7 AS static-php
ARG PHP_VERSION
ARG PHP_EXTENSIONS="apcu,bcmath,calendar,ctype,curl,dom,fileinfo,filter,gettext,iconv,ldap,mbstring,memcache,mysqli,opcache,openssl,pcntl,pdo,pdo_mysql,phar,posix,session,simplexml,sockets,tokenizer,xml,xmlreader,xmlwriter,xsl,zip,zlib"
ENV PHP_VERSION=${PHP_VERSION}
ENV PHP_EXTENSIONS=${PHP_EXTENSIONS}
ENV SPC_LIBC=glibc
ENV SPC_DEFAULT_C_FLAGS='-fPIE -fPIC -O3 -flax-vector-conversions'
ENV SPC_CMD_VAR_PHP_MAKE_EXTRA_LDFLAGS_PROGRAM='-Wl,-O3 -pie'
ENV SPC_CMD_VAR_PHP_MAKE_EXTRA_LIBS='-ldl -lpthread -lm -lresolv -lutil -lrt'

WORKDIR /opt/static-php

# Base build tooling for static-php-cli builds.
# CentOS 7 is EOL; pin vault repos and switch to altarch on aarch64.
RUN arch="$(uname -m)" \
    && if [ "$arch" = "aarch64" ]; then baseurl="https://vault.centos.org/altarch/7.9.2009"; \
       else baseurl="https://vault.centos.org/7.9.2009"; fi \
    && cat >/etc/yum.repos.d/CentOS-Base.repo <<EOF
[base]
name=CentOS-7 - Base
baseurl=${baseurl}/os/\$basearch/
gpgcheck=0
gpgkey=https://vault.centos.org/RPM-GPG-KEY-CentOS-7 ${baseurl}/os/\$basearch/RPM-GPG-KEY-CentOS-7

[updates]
name=CentOS-7 - Updates
baseurl=${baseurl}/updates/\$basearch/
gpgcheck=0
gpgkey=https://vault.centos.org/RPM-GPG-KEY-CentOS-7 ${baseurl}/os/\$basearch/RPM-GPG-KEY-CentOS-7

[extras]
name=CentOS-7 - Extras
baseurl=${baseurl}/extras/\$basearch/
gpgcheck=0
gpgkey=https://vault.centos.org/RPM-GPG-KEY-CentOS-7 ${baseurl}/os/\$basearch/RPM-GPG-KEY-CentOS-7
EOF
RUN arch="$(uname -m)" \
    && if [ "$arch" = "aarch64" ]; then baseurl="https://vault.centos.org/altarch/7.9.2009"; \
       else baseurl="https://vault.centos.org/7.9.2009"; fi \
    && rpm --import https://vault.centos.org/RPM-GPG-KEY-CentOS-7 \
    && rpm --import "${baseurl}/os/${arch}/RPM-GPG-KEY-CentOS-7"

RUN yum -y install \
        ca-certificates \
        curl \
        git \
        gcc \
        gcc-c++ \
        make \
        autoconf \
        automake \
        libtool \
        bison \
        flex \
        m4 \
        pkgconfig \
        cmake \
        patch \
        which \
        file \
        diffutils \
        findutils \
        tar \
        xz \
        gzip \
        bzip2 \
        unzip \
        perl \
        perl-IPC-Cmd \
        perl-Time-Piece \
        python3 \
    && yum clean all \
    && rm -rf /var/cache/yum

# Newer GCC for PHP 8.3 builds on CentOS 7 (devtoolset-10).
RUN yum -y install centos-release-scl \
    && if [ "$(uname -m)" = "aarch64" ]; then \
        sed -i 's|mirror.centos.org/centos|vault.centos.org/altarch|g' /etc/yum.repos.d/CentOS-SCLo-scl*.repo; \
        sed -i 's/^#.*baseurl=http/baseurl=http/g' /etc/yum.repos.d/CentOS-SCLo-scl*.repo; \
        sed -i 's/^mirrorlist=http/#mirrorlist=http/g' /etc/yum.repos.d/CentOS-SCLo-scl*.repo; \
    else \
        sed -i 's|mirror.centos.org/centos|vault.centos.org/centos|g' /etc/yum.repos.d/CentOS-SCLo-scl*.repo; \
        sed -i 's/^#.*baseurl=http/baseurl=http/g' /etc/yum.repos.d/CentOS-SCLo-scl*.repo; \
        sed -i 's/^mirrorlist=http/#mirrorlist=http/g' /etc/yum.repos.d/CentOS-SCLo-scl*.repo; \
    fi \
    && sed -i 's/^gpgcheck=1/gpgcheck=0/g' /etc/yum.repos.d/CentOS-SCLo-scl*.repo \
    && yum -y install devtoolset-10-gcc devtoolset-10-gcc-c++ \
    && yum clean all \
    && rm -rf /var/cache/yum

# Upgrade CMake for static-php-cli (CentOS 7 ships 2.8).
RUN arch="$(uname -m)" \
    && curl -fsSL -o cmake.tar.gz "https://github.com/Kitware/CMake/releases/download/v4.1.2/cmake-4.1.2-linux-${arch}.tar.gz" \
    && mkdir -p /cmake \
    && tar -xzf cmake.tar.gz -C /cmake --strip-components 1 \
    && rm -f cmake.tar.gz

# Install patchelf and re2c (not available in CentOS 7 base repos).
RUN curl -fsSL -o patchelf.tar.gz https://github.com/NixOS/patchelf/archive/refs/tags/0.12.tar.gz \
    && tar -xzf patchelf.tar.gz \
    && cd patchelf-0.12 \
    && (./configure || (autoreconf -fi && ./configure)) \
    && make -j "$(getconf _NPROCESSORS_ONLN)" \
    && make install \
    && cd /opt/static-php \
    && rm -rf patchelf-0.12 patchelf.tar.gz \
    && curl -fsSL -o re2c.tar.xz https://github.com/skvadrik/re2c/releases/download/2.2/re2c-2.2.tar.xz \
    && tar -xJf re2c.tar.xz \
    && cd re2c-2.2 \
    && ./configure --disable-dependency-tracking \
    && make -j "$(getconf _NPROCESSORS_ONLN)" \
    && make install \
    && cd /opt/static-php \
    && rm -rf re2c-2.2 re2c.tar.xz

ENV PATH="/opt/rh/devtoolset-10/root/usr/bin:/cmake/bin:${PATH}"
ENV CC="/opt/rh/devtoolset-10/root/usr/bin/gcc"
ENV CXX="/opt/rh/devtoolset-10/root/usr/bin/g++"

# Download the static-php-cli binary and build embed SAPI.
RUN arch="$(uname -m)" \
    && if [ "$arch" = "aarch64" ]; then spc_arch="aarch64"; else spc_arch="x86_64"; fi \
    && curl -fsSL -o spc "https://dl.static-php.dev/static-php-cli/spc-bin/nightly/spc-linux-${spc_arch}" \
    && chmod +x ./spc \
    && ./spc --version \
    && ./spc doctor --auto-fix \
    && ./spc download --with-php=${PHP_VERSION} --for-extensions="${PHP_EXTENSIONS}" \
    && ./spc build ${PHP_EXTENSIONS} --build-embed --debug --enable-zts

# Artifacts land in /opt/static-php/buildroot

# golang-base provides the Go toolchain for the static Go build stage.
# -----------------------------------------------------------------------------
FROM golang:${GO_VERSION}-bookworm AS golang-base

# build-go-static compiles the Valence Go binary against the static PHP embed lib.
# -----------------------------------------------------------------------------
FROM static-php AS build-go-static

COPY --from=golang-base /usr/local/go /usr/local/go
ENV PATH=/usr/local/go/bin:/opt/static-php/buildroot/bin:$PATH
ENV GOTOOLCHAIN=local

WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY cmd ./cmd
COPY internal ./internal
COPY --from=atom-build /out/atom.tar.gz /src/internal/atomembed/atom.tar.gz
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=1 \
    CGO_CFLAGS="$(php-config --includes) -DZTS -DZEND_ENABLE_STATIC_TSRMLS_CACHE=1 -pthread" \
    CGO_LDFLAGS="$(php-config --ldflags) /opt/static-php/buildroot/lib/libphp.a $(php-config --libs)" \
    go build -tags=nowatcher -o /out/valence ./cmd/valence

# runtime ships the Go binary plus the prebuilt legacy app.
# -----------------------------------------------------------------------------
FROM debian:bookworm-slim

RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates tzdata \
    && rm -rf /var/lib/apt/lists/*

RUN groupadd --system --gid 10001 valence \
    && useradd --system --uid 10001 --gid valence --home /app --shell /usr/sbin/nologin valence

WORKDIR /app
COPY --from=build-go-static --chown=valence:valence /out/valence /usr/local/bin/valence
COPY --from=atom-build --chown=valence:valence /app/atom /app/atom

ENV ATOM_DATA_DIR=/data/atom
ENV VALENCE_ATOM_SRC_DIR=/app/atom
RUN install -d -o valence -g valence \
        /data/atom \
        /data/atom/apps/qubit/config \
        /data/atom/cache \
        /data/atom/config \
        /data/atom/data \
        /data/atom/downloads \
        /data/atom/log \
        /data/atom/uploads

ENV VALENCE_ADDR=:8080

EXPOSE 8080
USER valence
CMD ["/usr/local/bin/valence"]
