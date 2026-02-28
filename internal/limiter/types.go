package limiter

import (
	"context"
	"net/http"
	"time"
)

type Priority uint8

const (
	PriorityNormal Priority = iota
	PriorityHigh
)

type Admission struct {
	Region   string
	Bucket   string
	Priority Priority
}

type Ticket struct {
	KeyIndex int
}

type Observation struct {
	Region     string
	Bucket     string
	KeyIndex   int
	StatusCode int
	Header     http.Header
}

type Clock interface {
	Now() time.Time
}

type MetricsSink interface {
	ObserveQueueDepth(bucket string, priority Priority, depth int)
	ObserveAdmission(wait time.Duration, outcome string)
}

type Config struct {
	KeyCount         int
	QueueCapacity    int
	AdditionalWindow time.Duration
	Clock            Clock
	Metrics          MetricsSink
	DefaultAppLimits string
}

type RejectedError struct {
	Reason     string
	RetryAfter time.Duration
}

func (e *RejectedError) Error() string {
	return "admission rejected: " + e.Reason
}

type admitRequest struct {
	ctx       context.Context
	admission Admission
	received  time.Time
	resp      chan admitResponse
}

type admitResponse struct {
	ticket Ticket
	err    error
}

type realClock struct{}

func (realClock) Now() time.Time {
	return time.Now()
}
