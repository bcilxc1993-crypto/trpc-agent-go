//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package tracesampler

import (
	"context"
	"math/rand"
	"strings"
	"sync/atomic"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	agenttrace "trpc.group/trpc-go/trpc-agent-go/agent/trace"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/plugin"
)

const (
	// defaultPluginName is the stable name returned by Name().
	defaultPluginName = "trace_sampler"
)

// context key types for passing state between BeforeAgent and AfterAgent.
type (
	sampledKey struct{} // value: bool — whether this run was sampled
)

// TraceSampler is a plugin.Plugin that aggregates a complete Runner execution
// into a single PromptIter Trace and reports it through a Reporter.
type TraceSampler struct {
	config      atomic.Pointer[Config]
	reporter    Reporter
	structureID string

	// randFloat is the random number generator used for sampling decisions.
	// Exposed for testing: callers can replace it with a deterministic
	// function via the unexported field.
	randFloat func() float64
}

// Compile-time interface assertion.
var _ plugin.Plugin = (*TraceSampler)(nil)

// New creates a TraceSampler with the given options.
func New(opts ...Option) *TraceSampler {
	ts := &TraceSampler{
		randFloat: rand.Float64,
	}
	// Store a default (disabled) config so that Load never returns nil.
	defaultCfg := &Config{}
	ts.config.Store(defaultCfg)

	for _, opt := range opts {
		if opt != nil {
			opt(ts)
		}
	}
	return ts
}

// Name implements plugin.Plugin.
func (ts *TraceSampler) Name() string {
	return defaultPluginName
}

// Register implements plugin.Plugin.
func (ts *TraceSampler) Register(r *plugin.Registry) {
	if ts == nil || r == nil {
		return
	}
	r.BeforeAgent(ts.beforeAgent)
	r.AfterAgent(ts.afterAgent)
}

// UpdateConfig atomically replaces the current configuration.
// The new configuration takes effect immediately for subsequent requests.
func (ts *TraceSampler) UpdateConfig(cfg Config) {
	ts.config.Store(&cfg)
}

// ---------------------------------------------------------------------------
// BeforeAgent callback
// ---------------------------------------------------------------------------

func (ts *TraceSampler) beforeAgent(
	ctx context.Context,
	args *agent.BeforeAgentArgs,
) (*agent.BeforeAgentResult, error) {
	if args == nil || args.Invocation == nil {
		return nil, nil
	}

	// If a parent already marked this run as sampled (or not), do nothing.
	// This ensures only the root agent makes the sampling decision.
	if _, ok := ctx.Value(sampledKey{}).(bool); ok {
		return nil, nil
	}

	// Determine if this is the root agent by checking the Branch field.
	// The root agent's Branch either is empty or contains no "/" delimiter.
	if isNestedBranch(args.Invocation.Branch) {
		return nil, nil
	}

	// Make the sampling decision.
	sampled := ts.shouldSample()
	ctx = context.WithValue(ctx, sampledKey{}, sampled)
	return &agent.BeforeAgentResult{Context: ctx}, nil
}

// ---------------------------------------------------------------------------
// AfterAgent callback
// ---------------------------------------------------------------------------

func (ts *TraceSampler) afterAgent(
	ctx context.Context,
	args *agent.AfterAgentArgs,
) (*agent.AfterAgentResult, error) {
	if args == nil || args.Invocation == nil {
		return nil, nil
	}

	// Only process for the root agent.
	if isNestedBranch(args.Invocation.Branch) {
		return nil, nil
	}

	// Check the sampling decision from BeforeAgent.
	sampled, ok := ctx.Value(sampledKey{}).(bool)
	if !ok || !sampled {
		return nil, nil
	}

	// Build the Trace from the framework's ExecutionTrace.
	t := ts.buildTrace(args)

	// Report — silently skip if no reporter is configured.
	ts.report(ctx, t)

	return nil, nil
}

// ---------------------------------------------------------------------------
// Trace construction
// ---------------------------------------------------------------------------

func (ts *TraceSampler) buildTrace(args *agent.AfterAgentArgs) *Trace {
	t := &Trace{
		StructureID: ts.structureID,
	}

	// Determine status.
	if args.Error != nil {
		t.Status = TraceStatusFailed
		return ts.buildTraceSteps(t, args)
	}

	if args.FullResponseEvent == nil || args.FullResponseEvent.ExecutionTrace == nil {
		t.Status = TraceStatusIncomplete
		t.Steps = []TraceStep{}
		return t
	}

	t.Status = TraceStatusCompleted
	t = ts.buildTraceSteps(t, args)

	// Extract final output text from the last event's response.
	t.FinalOutput = extractFinalOutput(args)

	return t
}

func (ts *TraceSampler) buildTraceSteps(t *Trace, args *agent.AfterAgentArgs) *Trace {
	if args.FullResponseEvent == nil || args.FullResponseEvent.ExecutionTrace == nil {
		t.Steps = []TraceStep{}
		return t
	}

	execTrace := args.FullResponseEvent.ExecutionTrace
	t.Steps = make([]TraceStep, 0, len(execTrace.Steps))
	for _, step := range execTrace.Steps {
		t.Steps = append(t.Steps, mapStep(step))
	}
	return t
}

// mapStep converts a framework trace.Step to a PromptIter TraceStep.
func mapStep(step agenttrace.Step) TraceStep {
	ts := TraceStep{
		StepID: step.StepID,
		NodeID: step.NodeID,
		Error:  step.Error,
	}

	// Copy PredecessorStepIDs.
	if len(step.PredecessorStepIDs) > 0 {
		ts.PredecessorStepIDs = make([]string, len(step.PredecessorStepIDs))
		copy(ts.PredecessorStepIDs, step.PredecessorStepIDs)
	}

	// Map Input snapshot.
	if step.Input != nil {
		ts.Input = &TraceInput{Text: step.Input.Text}
	}

	// Map Output snapshot.
	if step.Output != nil {
		ts.Output = &TraceOutput{Text: step.Output.Text}
	}

	return ts
}

// extractFinalOutput extracts the final response text from AfterAgentArgs.
func extractFinalOutput(args *agent.AfterAgentArgs) *TraceOutput {
	if args.FullResponseEvent == nil || args.FullResponseEvent.Response == nil {
		return nil
	}
	rsp := args.FullResponseEvent.Response
	if len(rsp.Choices) == 0 {
		return nil
	}

	// Try Message.Content first, then Delta.Content.
	text := rsp.Choices[0].Message.Content
	if text == "" {
		text = rsp.Choices[0].Delta.Content
	}
	if text == "" {
		return nil
	}
	return &TraceOutput{Text: text}
}

// ---------------------------------------------------------------------------
// Sampling logic
// ---------------------------------------------------------------------------

func (ts *TraceSampler) shouldSample() bool {
	cfg := ts.config.Load()
	if cfg == nil || !cfg.Enabled {
		return false
	}
	if cfg.SampleRate <= 0 {
		return false
	}
	if cfg.SampleRate >= 1 {
		return true
	}
	return ts.randFloat() < cfg.SampleRate
}

// ---------------------------------------------------------------------------
// Reporter
// ---------------------------------------------------------------------------

func (ts *TraceSampler) report(ctx context.Context, t *Trace) {
	if ts.reporter == nil {
		return
	}
	if err := ts.reporter.Report(ctx, t); err != nil {
		log.Warnf("tracesampler: reporter error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// isNestedBranch returns true if the branch indicates a nested (non-root) agent.
// A root agent has an empty branch or a branch without "/" separators.
func isNestedBranch(branch string) bool {
	return strings.Contains(branch, "/")
}
