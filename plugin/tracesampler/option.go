//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package tracesampler

// Config holds the runtime configuration for the TraceSampler plugin.
type Config struct {
	// Enabled controls whether sampling is active.
	Enabled bool

	// SampleRate is the probability [0, 1] that a given Runner execution
	// will be sampled. 0 means never, 1 means always.
	SampleRate float64
}

// Option configures a TraceSampler during construction.
type Option func(*TraceSampler)

// WithReporter sets the Reporter used to emit sampled Traces.
func WithReporter(r Reporter) Option {
	return func(ts *TraceSampler) {
		ts.reporter = r
	}
}

// WithConfig sets the initial configuration.
func WithConfig(cfg Config) Option {
	return func(ts *TraceSampler) {
		ts.config.Store(&cfg)
	}
}

// WithStructureID sets the StructureID stamped on every emitted Trace.
func WithStructureID(id string) Option {
	return func(ts *TraceSampler) {
		ts.structureID = id
	}
}
