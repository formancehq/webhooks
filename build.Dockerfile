FROM ghcr.io/formancehq/base:26.04
ARG TARGETPLATFORM
COPY $TARGETPLATFORM/webhooks /usr/bin/webhooks
ENV OTEL_SERVICE_NAME webhooks
ENTRYPOINT ["/usr/bin/webhooks"]
CMD ["server"]
