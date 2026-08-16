# goreleaser builds the static (CGO-off) binary; this image just wraps it.
# distroless/static ships the CA root bundle — needed for `pgbot explain`, whose
# HTTPS call fails an opaque x509 error without roots under sslmode=verify-full —
# plus a nonroot user, and has no shell or package manager. Because there is no
# RUN step, the per-arch images build under buildx without QEMU emulation.
FROM gcr.io/distroless/static:nonroot
COPY pgbot /usr/bin/pgbot
USER nonroot:nonroot
ENTRYPOINT ["/usr/bin/pgbot"]
