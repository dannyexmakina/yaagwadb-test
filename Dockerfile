FROM golang:1.26.2-bookworm AS build
WORKDIR /src
COPY --from=yaagwadb . .
RUN CGO_ENABLED=0 go build -o /yaagwadb ./cmd

FROM wordpress:6-php8.3-apache
RUN apt-get update \
 && apt-get install -y --no-install-recommends fuse3 \
 && rm -rf /var/lib/apt/lists/* \
 && echo user_allow_other >> /etc/fuse.conf
COPY --from=build /yaagwadb /usr/local/bin/yaagwadb
COPY config.toml /etc/yaagwadb/config.toml
COPY entrypoint.sh /usr/local/bin/yaagwadb-wp-entrypoint.sh
# Revert the official image's SIGWINCH stop signal to SIGTERM, handled by entrypoint.sh
STOPSIGNAL SIGTERM
ENTRYPOINT ["sh", "/usr/local/bin/yaagwadb-wp-entrypoint.sh"]
