VERSION 0.8

IMPORT github.com/formancehq/earthly:tags/v0.16.3 AS core

FROM core+base-image

CACHE --sharing=shared --id go-webhooks-cache /go/pkg/mod
CACHE --sharing=shared --id go-webhooks-cache /root/.cache/go-build

sources:
    FROM core+builder-image

    CACHE --id go-webhooks-cache /go/pkg/mod
    CACHE --id go-webhooks-cache /root/.cache/go-build

    WORKDIR /src
    COPY go.mod go.sum ./
    COPY --dir pkg cmd .
    COPY main.go .
    RUN go mod download
    SAVE ARTIFACT /src

compile:
    FROM core+builder-image
    
    CACHE --id go-webhooks-cache /go/pkg/mod
    CACHE --id go-webhooks-cache /root/.cache/go-build

    COPY (+sources/*) /src
    WORKDIR /src
    ARG VERSION=latest
    DO --pass-args core+GO_COMPILE --VERSION=$VERSION

build-image:
    FROM core+final-image
    ENTRYPOINT ["/bin/webhooks"]
    CMD ["serve"]
    COPY (+compile/main) /bin/webhooks
    ARG REPOSITORY=ghcr.io
    ARG tag=latest
    DO core+SAVE_IMAGE --COMPONENT=webhooks --REPOSITORY=${REPOSITORY} --TAG=$tag

deploy:
    COPY (+sources/*) /src
    LET tag=$(tar cf - /src | sha1sum | awk '{print $1}')
    WAIT
        BUILD --pass-args +build-image --tag=$tag
    END
    FROM --pass-args core+vcluster-deployer-image
    RUN kubectl patch Versions.formance.com default -p "{\"spec\":{\"webhooks\": \"${tag}\"}}" --type=merge

deploy-staging:
    BUILD --pass-args core+deploy-staging

openapi:
    COPY ./openapi.yaml .
    SAVE ARTIFACT ./openapi.yaml