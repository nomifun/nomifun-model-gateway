# SPDX-License-Identifier: Apache-2.0
# Official image manifest digests verified against registry-1.docker.io.
FROM node:24.18.0-bookworm-slim@sha256:6f7b03f7c2c8e2e784dcf9295400527b9b1270fd37b7e9a7285cf83b6951452d AS console
WORKDIR /src/console
COPY console/package.json console/package-lock.json ./
RUN npm ci --ignore-scripts
COPY console/ ./
RUN npm run build
COPY scripts/check-frontend-licenses.mjs scripts/collect-frontend-notices.mjs /src/scripts/
COPY LICENSE /src/LICENSE
COPY licenses/ /src/licenses/
RUN node /src/scripts/collect-frontend-notices.mjs /src/console /src/frontend-notices.txt

FROM golang:1.27.1-bookworm@sha256:8d48e12ec56735e9358640898b9d9b9fcca110612ed8a5567438c0a1baa24e66 AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY LICENSE NOTICE ./
COPY cmd/model-gateway/ ./cmd/model-gateway/
COPY internal/ ./internal/
COPY contract/ ./contract/
COPY --from=console /src/internal/console/assets ./internal/console/assets
ARG TARGETOS=linux
ARG TARGETARCH=amd64
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath -ldflags="-s -w" -o /out/model-gateway ./cmd/model-gateway
RUN go install github.com/google/go-licenses/v2@v2.0.1
RUN go-licenses check --allowed_licenses=Apache-2.0,MIT,BSD-2-Clause,BSD-3-Clause,0BSD,ISC,Unlicense,CC0-1.0 ./cmd/model-gateway
RUN go-licenses save ./cmd/model-gateway --save_path=/out/dependency-licenses
RUN mkdir -p /data && chmod 0700 /data

FROM scratch
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=builder /out/model-gateway /model-gateway
COPY --from=builder --chown=10001:10001 /data /data
COPY LICENSE NOTICE /licenses/
COPY --from=builder /out/dependency-licenses /licenses/go/
COPY --from=console /src/frontend-notices.txt /licenses/frontend.txt
COPY licenses/frontend/ /licenses/frontend-sources/
ENV HOME=/data NMG_LISTEN=0.0.0.0:8789
USER 10001:10001
EXPOSE 8789
HEALTHCHECK --interval=30s --timeout=5s --start-period=40s --retries=3 CMD ["/model-gateway", "--healthcheck"]
ENTRYPOINT ["/model-gateway"]
