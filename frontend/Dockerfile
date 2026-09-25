FROM node:24-alpine AS build

WORKDIR /src/frontend
COPY frontend/package.json frontend/package-lock.json ./
RUN --mount=type=cache,target=/root/.npm npm ci
COPY . /src/

ARG VITE_AI_ENABLED=false
ENV VITE_AI_ENABLED=${VITE_AI_ENABLED}
ARG VERSION=dev
RUN npm run build && printf '%s\n' "$VERSION" > dist/release.txt

FROM nginxinc/nginx-unprivileged:1.29-alpine

USER root
RUN mkdir -p /var/cache/easygpa/assets && chown -R 101:101 /var/cache/easygpa
COPY --chmod=755 deploy/nginx/publish-assets.sh /docker-entrypoint.d/40-publish-assets.sh
USER 101

COPY deploy/nginx/default.conf /etc/nginx/conf.d/default.conf
COPY --from=build /src/frontend/dist /usr/share/nginx/html

EXPOSE 8080
