// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package aws provides functions to trace aws/aws-sdk-go (https://github.com/aws/aws-sdk-go).
package aws // import "gopkg.in/DataDog/dd-trace-go.v1/contrib/aws/aws-sdk-go/aws"

import (
	"math"
	"strconv"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"

	"github.com/aws/aws-sdk-go/aws/request"
	"github.com/aws/aws-sdk-go/aws/session"
)

const (
	tagAWSAgent      = "aws.agent"
	tagAWSOperation  = "aws.operation"
	tagAWSRegion     = "aws.region"
	tagAWSRetryCount = "aws.retry_count"
	tagAWSRequestID  = "aws.request_id"
	// SendHandlerName is the name of the Datadog NamedHandler for the Send phase of an awsv1 request
	SendHandlerName = "gopkg.in/DataDog/dd-trace-go.v1/contrib/aws/aws-sdk-go/aws/handlers.Send"
	// CompleteHandlerName is the name of the Datadog NamedHandler for the Complete phase of an awsv1 request
	CompleteHandlerName = "gopkg.in/DataDog/dd-trace-go.v1/contrib/aws/aws-sdk-go/aws/handlers.Complete"
)

type handlers struct {
	cfg *config
}

// WrapSession wraps a session.Session, causing requests and responses to be traced.
func WrapSession(s *session.Session, opts ...Option) *session.Session {
	cfg := new(config)
	defaults(cfg)
	for _, opt := range opts {
		opt(cfg)
	}
	log.Debug("contrib/aws/aws-sdk-go/aws: Wrapping Session: %#v", cfg)
	h := &handlers{cfg: cfg}
	s = s.Copy()
	s.Handlers.Send.PushFrontNamed(request.NamedHandler{
		Name: SendHandlerName,
		Fn:   h.Send,
	})
	s.Handlers.Complete.PushBackNamed(request.NamedHandler{
		Name: CompleteHandlerName,
		Fn:   h.Complete,
	})
	return s
}

func (h *handlers) Send(req *request.Request) {
	if req.RetryCount != 0 {
		return
	}
	// Make a copy of the URL so we don't modify the outgoing request
	url := *req.HTTPRequest.URL
	url.User = nil // Do not include userinfo in the HTTPURL tag.
	opts := []ddtrace.StartSpanOption{
		tracer.SpanType(ext.SpanTypeHTTP),
		tracer.ServiceName(h.serviceName(req)),
		tracer.ResourceName(h.resourceName(req)),
		tracer.Tag(tagAWSAgent, h.awsAgent(req)),
		tracer.Tag(tagAWSOperation, h.awsOperation(req)),
		tracer.Tag(tagAWSRegion, h.awsRegion(req)),
		tracer.Tag(ext.HTTPMethod, req.Operation.HTTPMethod),
		tracer.Tag(ext.HTTPURL, url.String()),
		tracer.Tag(ext.Component, "aws/aws-sdk-go/aws"),
		tracer.Tag(ext.SpanKind, ext.SpanKindClient),
	}
	if !math.IsNaN(h.cfg.analyticsRate) {
		opts = append(opts, tracer.Tag(ext.EventSampleRate, h.cfg.analyticsRate))
	}
	_, ctx := tracer.StartSpanFromContext(req.Context(), h.operationName(req), opts...)
	req.SetContext(ctx)
}

func (h *handlers) Complete(req *request.Request) {
	span, ok := tracer.SpanFromContext(req.Context())
	if !ok {
		return
	}
	span.SetTag(tagAWSRetryCount, req.RetryCount)
	span.SetTag(tagAWSRequestID, req.RequestID)
	if req.HTTPResponse != nil {
		span.SetTag(ext.HTTPCode, strconv.Itoa(req.HTTPResponse.StatusCode))
	}
	span.Finish(tracer.WithError(req.Error))
}

func (h *handlers) operationName(req *request.Request) string {
	return h.awsService(req) + ".command"
}

func (h *handlers) resourceName(req *request.Request) string {
	return h.awsService(req) + "." + req.Operation.Name
}

func (h *handlers) serviceName(req *request.Request) string {
	if h.cfg.serviceName != "" {
		return h.cfg.serviceName
	}
	return "aws." + h.awsService(req)
}

func (h *handlers) awsAgent(req *request.Request) string {
	if agent := req.HTTPRequest.Header.Get("User-Agent"); agent != "" {
		return agent
	}
	return "aws-sdk-go"
}

func (h *handlers) awsOperation(req *request.Request) string {
	return req.Operation.Name
}

func (h *handlers) awsRegion(req *request.Request) string {
	return req.ClientInfo.SigningRegion
}

func (h *handlers) awsService(req *request.Request) string {
	return req.ClientInfo.ServiceName
}
