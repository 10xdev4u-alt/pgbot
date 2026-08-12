# goreleaser builds the static binary; this image just wraps it. CGO is
# disabled, so a scratch base is enough — no libc needed.
FROM alpine:3.20
RUN adduser -D -u 10001 pgbot
COPY pgbot /usr/bin/pgbot
USER pgbot
ENTRYPOINT ["/usr/bin/pgbot"]
