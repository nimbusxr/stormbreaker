# The courier's mock image: WireMock mappings, data only. In an environment it is FROM scratch and
# mounted read-only as an image volume; the lab copies it in with an init container, which needs a
# shell, so it is built on busybox here.
FROM busybox:1.37-musl
COPY mappings /mocks/default/mappings
