## syntax=docker/dockerfile:1
#
###
### Build
###
#FROM golang:1.18-alpine AS build
#
#WORKDIR /app
#
#COPY go.mod ./
#RUN go mod download
#
#COPY . ./
#
#RUN CGO_ENABLED=0 GOOS=linux go build -o /master-docker-go-server/cmd/main,go
#
###
### Deploy
###
#FROM scratch
#
#WORKDIR /
#
#COPY --from=build /master-docker-go-server/cmd /master-docker-go-server/cmd
#
#EXPOSE 9099
#
#ENTRYPOINT ["/master-docker-go-server/"]

FROM golang:1.18-alpine AS build
WORKDIR /app
ADD . /app

# 以下两者仅存其一
# m
EXPOSE 9099
RUN CGO_ENABLED=0 GOOS=linux go build ./cmd/main.go

# sm
#EXPOSE 9100
#RUN CGO_ENABLED=0 GOOS=linux go build ./shadow_master/cmd/main.go


ENTRYPOINT ["./main"]