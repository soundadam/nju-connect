# Shared configuration
GO ?= go
BINARY := bin/soundconnect
PLATFORMS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64
BENCHTIME ?= 250ms
BENCHCOUNT ?= 5
LEAKCOUNT ?= 10
VERSION ?=
UI_LANGUAGE ?= en

# EasyConnect research configuration
RESEARCH_ROOT := research
EASYCONNECT_UPSTREAM := $(RESEARCH_ROOT)/upstream/easyconnect
EASYCONNECT_WORK := $(RESEARCH_ROOT)/work/easyconnect
EASYCONNECT_LOCK := $(RESEARCH_ROOT)/lock.json
PACKAGE ?= $(firstword $(wildcard $(EASYCONNECT_UPSTREAM)/package/*.deb))
EASYCONNECT_DEPENDENCY ?= $(firstword $(wildcard $(EASYCONNECT_UPSTREAM)/dependencies/*.deb))

# Target groups
include make/build.mk
include make/release.mk
include make/research.mk
