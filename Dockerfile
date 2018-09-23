FROM scratch

ADD server /opt/pull-request-auto-merger/bin/server

EXPOSE 8080

ENTRYPOINT ["/opt/pull-request-auto-merger/bin/server"]
