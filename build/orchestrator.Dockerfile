# The orchestrator's image. GoReleaser builds the binary; this only packages it.
# It runs as UID 1001, and also under an arbitrary UID in group 0 (OpenShift): /data, where the
# orchestrator keeps its database, belongs to group 0 and is group-writable.
FROM busybox:1.37-musl AS data
RUN mkdir -p /data

FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
ARG TARGETPLATFORM
COPY ${TARGETPLATFORM}/stormbreaker /usr/local/bin/stormbreaker
COPY --from=data --chown=1001:0 --chmod=0775 /data /data
ENV STORMBREAKER_DB=/data/orchestrator.db
USER 1001:0
VOLUME /data
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/stormbreaker", "orchestrator", "serve"]
