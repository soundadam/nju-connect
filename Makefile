# Shared configuration
GO ?= go
BINARY := bin/nju-connect
PLATFORMS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64
BENCHTIME ?= 250ms
BENCHCOUNT ?= 5
LEAKCOUNT ?= 10
VERSION ?=

# Target groups
include make/build.mk
include make/release.mk
