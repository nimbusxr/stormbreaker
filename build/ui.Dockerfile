# The UI's image (ADR 0026). GoReleaser builds the binary; this only packages it. It keeps
# nothing, and runs as UID 1001, or under an arbitrary UID (OpenShift).
FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
ARG TARGETPLATFORM
COPY ${TARGETPLATFORM}/stormbreaker /usr/local/bin/stormbreaker
USER 1001:0
EXPOSE 8095
ENTRYPOINT ["/usr/local/bin/stormbreaker", "ui", "serve"]
