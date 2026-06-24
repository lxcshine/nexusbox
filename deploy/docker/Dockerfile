FROM golang:1.22-bookworm AS builder

WORKDIR /src
ENV GOPROXY=https://goproxy.cn,direct
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /usr/local/bin/nexusbox-agent ./cmd/sandbox-dev

# ---- Runtime ----
FROM debian:bookworm-slim

# Use Aliyun mirror for apt
RUN sed -i 's|deb.debian.org|mirrors.aliyun.com|g' /etc/apt/sources.list.d/debian.sources 2>/dev/null || \
    (echo "deb http://mirrors.aliyun.com/debian/ bookworm main contrib non-free non-free-firmware" > /etc/apt/sources.list && \
     echo "deb http://mirrors.aliyun.com/debian/ bookworm-updates main contrib non-free non-free-firmware" >> /etc/apt/sources.list && \
     echo "deb http://mirrors.aliyun.com/debian-security/ bookworm-security main contrib non-free non-free-firmware" >> /etc/apt/sources.list)

RUN apt-get update && apt-get install -y --no-install-recommends \
    supervisor \
    curl \
    ca-certificates \
    tigervnc-standalone-server \
    tigervnc-common \
    chromium \
    chromium-common \
    python3 \
    python3-pip \
    xdg-utils \
    fonts-noto-cjk \
    build-essential \
    && apt-get clean \
    && rm -rf /var/lib/apt/lists/*

# Install Node.js 22 from NodeSource
RUN curl -fsSL https://deb.nodesource.com/setup_22.x | bash - && \
    apt-get install -y nodejs && \
    apt-get clean

# Install Python packages with Tsinghua mirror
RUN pip3 install --no-cache-dir --break-system-packages -i https://pypi.tuna.tsinghua.edu.cn/simple \
    jupyterlab \
    websockify

# Install code-server from pre-built binary
COPY deploy/docker/code-server-4.106.3-linux-amd64.tar.gz /tmp/
RUN tar xzf /tmp/code-server-4.106.3-linux-amd64.tar.gz -C /opt && \
    ln -s /opt/code-server-4.106.3-linux-amd64/bin/code-server /usr/local/bin/code-server && \
    rm /tmp/code-server-4.106.3-linux-amd64.tar.gz

# Install noVNC from local tarball
COPY deploy/docker/noVNC-1.4.0.tar.gz /tmp/
RUN tar xzf /tmp/noVNC-1.4.0.tar.gz -C /opt && \
    mv /opt/noVNC-1.4.0 /usr/share/novnc && \
    ln -s /usr/share/novnc/vnc.html /usr/share/novnc/index.html && \
    rm /tmp/noVNC-1.4.0.tar.gz

COPY --from=builder /usr/local/bin/nexusbox-agent /usr/local/bin/nexusbox-agent
COPY deploy/docker/supervisord.conf /etc/supervisor/conf.d/nexusbox.conf
COPY deploy/docker/nexusbox-entrypoint.sh /usr/local/bin/nexusbox-entrypoint.sh
RUN chmod +x /usr/local/bin/nexusbox-entrypoint.sh

# Create log directory
RUN mkdir -p /var/log/supervisor /var/log/nexusbox

# Create sandbox user and workspace
RUN useradd -m -s /bin/bash sandbox \
    && mkdir -p /home/sandbox \
    && chown sandbox:sandbox /home/sandbox

# Environment
ENV WORKSPACE=/home/sandbox \
    PUBLIC_PORT=8080 \
    MCP_HUB_PORT=8079 \
    PROXY_PORT=6081 \
    WEBSOCKET_PROXY_PORT=6080 \
    JUPYTER_LAB_PORT=8888 \
    CODE_SERVER_PORT=8200 \
    BROWSER_REMOTE_DEBUGGING_PORT=9222 \
    VNC_SERVER_PORT=5900 \
    LOG_LEVEL=info

# Ports
EXPOSE 8080 6080 6081 8079 8888 8200 9222 5900

# Health check
HEALTHCHECK --interval=30s --timeout=5s --start-period=15s --retries=3 \
    CMD curl -f http://localhost:8080/healthz || exit 1

# Volumes
VOLUME ["/home/sandbox"]

ENTRYPOINT ["/usr/bin/supervisord", "-c", "/etc/supervisor/conf.d/nexusbox.conf"]
