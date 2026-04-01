//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package tracesampler

import "context"

// Reporter is the interface that users implement to receive sampled Traces.
// The plugin calls Report synchronously in the AfterAgent callback.
// Implementations that need asynchronous delivery should handle buffering
// internally.
type Reporter interface {
	// Report delivers a sampled Trace. The context is the same context
	// from the AfterAgent callback.
	Report(ctx context.Context, trace *Trace) error
}
