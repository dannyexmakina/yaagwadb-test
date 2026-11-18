FROM debian:bookworm-slim

RUN apt-get update \
 && apt-get install -y --no-install-recommends fuse3 \
 && rm -rf /var/lib/apt/lists/*

COPY develop.sh /usr/local/bin/develop.sh

CMD ["sh", "/usr/local/bin/develop.sh"]
