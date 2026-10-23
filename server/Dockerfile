# moefrp-server 镜像。构建上下文是仓库根目录（server 通过 replace 引用 ../shared）：
#   docker build -f server/Dockerfile -t moefrp-server .
# GitHub Actions 在 x86 和 ARM 的原生机器上分别构建（不用 QEMU），再合并成多架构镜像。

FROM golang:1.26-alpine AS build
WORKDIR /src
COPY shared/ shared/
COPY server/go.mod server/go.sum server/
RUN cd server && go mod download
COPY server/ server/
ARG VERSION=dev
RUN cd server && CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/moefrp-server ./cmd/moefrp-server \
 && mkdir -p /out/data

# distroless：没有 shell 和包管理器，以 nonroot（uid 65532）运行，自带 CA 证书和时区数据
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/moefrp-server /usr/local/bin/moefrp-server
# 所有数据都在 /data：数据库、可选的 config.toml、升级前的自动备份。挂载宿主机目录即可整体带走。
# 目录归 nonroot 所有（命名卷第一次挂载时会继承这个属主；绑定挂载的目录属主由宿主机决定）
COPY --from=build --chown=65532:65532 /out/data /data
# 容器里监听所有地址，对外暴露在哪由端口映射决定；环境变量 MOEFRP_* 优先于 config.toml
ENV MOEFRP_LISTEN=0.0.0.0:8080 \
    MOEFRP_DB_PATH=/data/moefrp.db
EXPOSE 8080
VOLUME ["/data"]
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 CMD ["moefrp-server", "health"]
ENTRYPOINT ["moefrp-server"]
# --auto-migrate：升级镜像后自动执行数据库迁移；已有数据时会先自动备份到 /data/*.pre-migrate-*.db
CMD ["serve", "--auto-migrate"]
