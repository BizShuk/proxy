package model

import "net/http"

// RequestEnvelope carries one protocol request through routing and transforms.
type RequestEnvelope struct {
	SourceFormat Format
	TargetFormat Format
	Model        string
	Stream       bool
	Headers      http.Header
	Body         []byte
}

// Warning describes a safe, non-fatal conversion warning.
type Warning struct {
	Code    string
	Message string
}

// SemanticLoss describes information that cannot be represented exactly.
type SemanticLoss struct {
	Field  string
	Reason string
}

// TransformResult is the output of a request or non-stream response transform.
type TransformResult struct {
	Body     []byte
	Warnings []Warning
	Losses   []SemanticLoss
}

// Exchange contains the request-scoped state needed by reverse transforms.
type Exchange struct {
	OriginalRequest   RequestEnvelope
	TranslatedRequest RequestEnvelope
	ProviderID        string
	NewID             func() string
}

// ResponseEnvelope carries one successful upstream response to a reverse transform.
type ResponseEnvelope struct {
	Status   int
	Headers  http.Header
	Body     []byte
	Exchange Exchange
}
