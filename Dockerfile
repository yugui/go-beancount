FROM debian:latest
ARG BAZELISK_VERSION=1.28.1

RUN useradd -m -G sudo yugui && \
    echo 'export PATH="$HOME/.local/bin:$HOME/go/bin:$PATH"' >> ~yugui/.bashrc && \
    mkdir -p /etc/sudoers.d && \
    echo 'yugui ALL=(ALL) NOPASSWD:ALL' >> /etc/sudoers.d/yugui
RUN apt update
RUN apt install -y --no-install-recommends \
    build-essential \
    git \
    golang \
    nodejs \
    less \
    vim \
    curl \
    wget \
    gpg \
    ca-certificates \
    sudo \
    jq \
    protobuf-compiler
RUN curl -fsSL -o bazelisk.deb https://github.com/bazelbuild/bazelisk/releases/download/v$BAZELISK_VERSION/bazelisk-arm64.deb && dpkg -i bazelisk.deb && rm bazelisk.deb
RUN sudo -u yugui env GOENV=/home/yugui/go go install github.com/bazelbuild/buildtools/buildifier@latest
RUN curl -fsSL https://claude.ai/install.sh | sudo -u yugui bash 
USER yugui
WORKDIR /home/yugui
