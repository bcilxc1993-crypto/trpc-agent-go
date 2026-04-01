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
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	agenttrace "trpc.group/trpc-go/trpc-agent-go/agent/trace"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/plugin"
)

// ---------------------------------------------------------------------------
// Mock Reporter
// ---------------------------------------------------------------------------

type mockReporter struct {
	mu     sync.Mutex
	traces []*Trace
	err    error // error to return from Report
}

func (m *mockReporter) Report(_ context.Context, t *Trace) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.traces = append(m.traces, t)
	return m.err
}

func (m *mockReporter) lastTrace() *Trace {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.traces) == 0 {
		return nil
	}
	return m.traces[len(m.traces)-1]
}

func (m *mockReporter) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.traces)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func newTestPlugin(reporter *mockReporter, cfg Config, opts ...Option) *TraceSampler {
	allOpts := []Option{
		WithReporter(reporter),
		WithConfig(cfg),
	}
	allOpts = append(allOpts, opts...)
	return New(allOpts...)
}

// rootInvocation returns an Invocation that looks like a root agent.
func rootInvocation() *agent.Invocation {
	return &agent.Invocation{
		AgentName:    "root-agent",
		InvocationID: "inv-001",
		Branch:       "root-agent",
	}
}

// nestedInvocation returns an Invocation that looks like a nested (child) agent.
func nestedInvocation() *agent.Invocation {
	return &agent.Invocation{
		AgentName:    "child-agent",
		InvocationID: "inv-002",
		Branch:       "root-agent/child-agent",
	}
}

// sampleExecutionTrace returns a framework trace.Trace with a few steps.
func sampleExecutionTrace() *agenttrace.Trace {
	return &agenttrace.Trace{
		RootAgentName:    "root-agent",
		RootInvocationID: "inv-001",
		Status:           agenttrace.TraceStatusCompleted,
		Steps: []agenttrace.Step{
			{
				StepID:             "step-1",
				InvocationID:       "inv-001",
				AgentName:          "root-agent",
				NodeID:             "node-start",
				PredecessorStepIDs: nil,
				Input:              &agenttrace.Snapshot{Text: "user input"},
				Output:             &agenttrace.Snapshot{Text: "model output"},
			},
			{
				StepID:             "step-2",
				InvocationID:       "inv-001",
				AgentName:          "root-agent",
				NodeID:             "node-tool",
				PredecessorStepIDs: []string{"step-1"},
				Input:              &agenttrace.Snapshot{Text: "tool input"},
				Output:             &agenttrace.Snapshot{Text: "tool output"},
			},
			{
				StepID:             "step-3",
				InvocationID:       "inv-001",
				AgentName:          "root-agent",
				NodeID:             "node-end",
				PredecessorStepIDs: []string{"step-1", "step-2"},
				Input:              &agenttrace.Snapshot{Text: "final input"},
				Output:             &agenttrace.Snapshot{Text: "final output"},
			},
		},
	}
}

func makeFullResponseEvent(execTrace *agenttrace.Trace, responseText string) *event.Event {
	e := &event.Event{
		Response: &model.Response{
			Choices: []model.Choice{
				{Message: model.Message{Content: responseText}},
			},
		},
		ExecutionTrace: execTrace,
	}
	return e
}

// runFullCycle simulates a full BeforeAgent -> AfterAgent cycle and returns
// the context produced by BeforeAgent.
func runFullCycle(
	ctx context.Context,
	ts *TraceSampler,
	inv *agent.Invocation,
	afterArgs *agent.AfterAgentArgs,
) context.Context {
	beforeResult, _ := ts.beforeAgent(ctx, &agent.BeforeAgentArgs{
		Invocation: inv,
	})
	if beforeResult != nil && beforeResult.Context != nil {
		ctx = beforeResult.Context
	}
	afterArgs.Invocation = inv
	ts.afterAgent(ctx, afterArgs) //nolint: errcheck
	return ctx
}

// ===================================================================
// 4.1 Plugin registration, Plugin interface, Name return value
// ===================================================================

func TestPluginInterface(t *testing.T) {
	ts := New()
	assert.Equal(t, "trace_sampler", ts.Name())

	// Verify it satisfies plugin.Plugin.
	var p plugin.Plugin = ts
	assert.NotNil(t, p)
}

func TestPluginRegistration(t *testing.T) {
	reporter := &mockReporter{}
	ts := newTestPlugin(reporter, Config{Enabled: true, SampleRate: 1})

	mgr, err := plugin.NewManager(ts)
	require.NoError(t, err)
	require.NotNil(t, mgr)

	// AgentCallbacks must be registered.
	assert.NotNil(t, mgr.AgentCallbacks())
}

func TestRegisterNilSafety(t *testing.T) {
	var ts *TraceSampler
	// Should not panic.
	ts.Register(nil)

	ts2 := New()
	ts2.Register(nil)
}

// ===================================================================
// 4.2 Sampling rate tests
// ===================================================================

func TestSampleRate_Zero_NoReport(t *testing.T) {
	reporter := &mockReporter{}
	ts := newTestPlugin(reporter, Config{Enabled: true, SampleRate: 0})

	inv := rootInvocation()
	ctx := context.Background()
	afterArgs := &agent.AfterAgentArgs{
		FullResponseEvent: makeFullResponseEvent(sampleExecutionTrace(), "hello"),
	}
	runFullCycle(ctx, ts, inv, afterArgs)

	assert.Equal(t, 0, reporter.count(), "SampleRate=0 should never report")
}

func TestSampleRate_One_AlwaysReport(t *testing.T) {
	reporter := &mockReporter{}
	ts := newTestPlugin(reporter, Config{Enabled: true, SampleRate: 1})

	inv := rootInvocation()
	for i := 0; i < 10; i++ {
		ctx := context.Background()
		afterArgs := &agent.AfterAgentArgs{
			FullResponseEvent: makeFullResponseEvent(sampleExecutionTrace(), "hello"),
		}
		runFullCycle(ctx, ts, inv, afterArgs)
	}

	assert.Equal(t, 10, reporter.count(), "SampleRate=1 should always report")
}

func TestEnabled_False_NoReport(t *testing.T) {
	reporter := &mockReporter{}
	ts := newTestPlugin(reporter, Config{Enabled: false, SampleRate: 1})

	inv := rootInvocation()
	ctx := context.Background()
	afterArgs := &agent.AfterAgentArgs{
		FullResponseEvent: makeFullResponseEvent(sampleExecutionTrace(), "hello"),
	}
	runFullCycle(ctx, ts, inv, afterArgs)

	assert.Equal(t, 0, reporter.count(), "Enabled=false should never report")
}

func TestSampleRate_Probabilistic(t *testing.T) {
	reporter := &mockReporter{}
	ts := newTestPlugin(reporter, Config{Enabled: true, SampleRate: 0.5})

	// Replace randFloat with a deterministic sequence.
	callCount := 0
	ts.randFloat = func() float64 {
		callCount++
		if callCount%2 == 0 {
			return 0.3 // < 0.5 → sampled
		}
		return 0.7 // >= 0.5 → not sampled
	}

	inv := rootInvocation()
	for i := 0; i < 10; i++ {
		ctx := context.Background()
		afterArgs := &agent.AfterAgentArgs{
			FullResponseEvent: makeFullResponseEvent(sampleExecutionTrace(), "hello"),
		}
		runFullCycle(ctx, ts, inv, afterArgs)
	}

	// Odd calls: not sampled, Even calls: sampled → 5 out of 10.
	assert.Equal(t, 5, reporter.count())
}

// ===================================================================
// 4.3 Trace construction tests
// ===================================================================

func TestTrace_Completed(t *testing.T) {
	reporter := &mockReporter{}
	ts := newTestPlugin(reporter, Config{Enabled: true, SampleRate: 1},
		WithStructureID("struct-001"))

	inv := rootInvocation()
	ctx := context.Background()
	afterArgs := &agent.AfterAgentArgs{
		FullResponseEvent: makeFullResponseEvent(sampleExecutionTrace(), "final answer"),
	}
	runFullCycle(ctx, ts, inv, afterArgs)

	require.Equal(t, 1, reporter.count())
	tr := reporter.lastTrace()

	assert.Equal(t, TraceStatusCompleted, tr.Status)
	assert.Equal(t, "struct-001", tr.StructureID)
	assert.Equal(t, 3, len(tr.Steps))
	require.NotNil(t, tr.FinalOutput)
	assert.Equal(t, "final answer", tr.FinalOutput.Text)
}

func TestTrace_Failed(t *testing.T) {
	reporter := &mockReporter{}
	ts := newTestPlugin(reporter, Config{Enabled: true, SampleRate: 1})

	inv := rootInvocation()
	ctx := context.Background()
	afterArgs := &agent.AfterAgentArgs{
		FullResponseEvent: makeFullResponseEvent(sampleExecutionTrace(), ""),
		Error:             errors.New("something went wrong"),
	}
	runFullCycle(ctx, ts, inv, afterArgs)

	require.Equal(t, 1, reporter.count())
	tr := reporter.lastTrace()
	assert.Equal(t, TraceStatusFailed, tr.Status)
}

func TestTrace_Incomplete_NilExecutionTrace(t *testing.T) {
	reporter := &mockReporter{}
	ts := newTestPlugin(reporter, Config{Enabled: true, SampleRate: 1})

	inv := rootInvocation()
	ctx := context.Background()
	afterArgs := &agent.AfterAgentArgs{
		FullResponseEvent: &event.Event{
			Response:       &model.Response{},
			ExecutionTrace: nil,
		},
	}
	runFullCycle(ctx, ts, inv, afterArgs)

	require.Equal(t, 1, reporter.count())
	tr := reporter.lastTrace()
	assert.Equal(t, TraceStatusIncomplete, tr.Status)
	assert.Empty(t, tr.Steps)
}

func TestTrace_Incomplete_NilFullResponseEvent(t *testing.T) {
	reporter := &mockReporter{}
	ts := newTestPlugin(reporter, Config{Enabled: true, SampleRate: 1})

	inv := rootInvocation()
	ctx := context.Background()
	afterArgs := &agent.AfterAgentArgs{
		FullResponseEvent: nil,
	}
	runFullCycle(ctx, ts, inv, afterArgs)

	require.Equal(t, 1, reporter.count())
	tr := reporter.lastTrace()
	assert.Equal(t, TraceStatusIncomplete, tr.Status)
	assert.Empty(t, tr.Steps)
}

// ===================================================================
// 4.4 Step-DAG mapping tests
// ===================================================================

func TestStepDAG_StepID_Uniqueness(t *testing.T) {
	reporter := &mockReporter{}
	ts := newTestPlugin(reporter, Config{Enabled: true, SampleRate: 1})

	inv := rootInvocation()
	ctx := context.Background()
	afterArgs := &agent.AfterAgentArgs{
		FullResponseEvent: makeFullResponseEvent(sampleExecutionTrace(), "ok"),
	}
	runFullCycle(ctx, ts, inv, afterArgs)

	require.Equal(t, 1, reporter.count())
	tr := reporter.lastTrace()

	ids := make(map[string]struct{})
	for _, step := range tr.Steps {
		_, exists := ids[step.StepID]
		assert.False(t, exists, "duplicate StepID: %s", step.StepID)
		ids[step.StepID] = struct{}{}
	}
}

func TestStepDAG_PredecessorStepIDs(t *testing.T) {
	reporter := &mockReporter{}
	ts := newTestPlugin(reporter, Config{Enabled: true, SampleRate: 1})

	inv := rootInvocation()
	ctx := context.Background()
	afterArgs := &agent.AfterAgentArgs{
		FullResponseEvent: makeFullResponseEvent(sampleExecutionTrace(), "ok"),
	}
	runFullCycle(ctx, ts, inv, afterArgs)

	require.Equal(t, 1, reporter.count())
	tr := reporter.lastTrace()

	// step-1 has no predecessors.
	assert.Empty(t, tr.Steps[0].PredecessorStepIDs)

	// step-2 depends on step-1.
	assert.Equal(t, []string{"step-1"}, tr.Steps[1].PredecessorStepIDs)

	// step-3 depends on step-1 and step-2.
	assert.Equal(t, []string{"step-1", "step-2"}, tr.Steps[2].PredecessorStepIDs)
}

func TestStepDAG_InputOutput_Mapping(t *testing.T) {
	reporter := &mockReporter{}
	ts := newTestPlugin(reporter, Config{Enabled: true, SampleRate: 1})

	inv := rootInvocation()
	ctx := context.Background()
	afterArgs := &agent.AfterAgentArgs{
		FullResponseEvent: makeFullResponseEvent(sampleExecutionTrace(), "ok"),
	}
	runFullCycle(ctx, ts, inv, afterArgs)

	require.Equal(t, 1, reporter.count())
	tr := reporter.lastTrace()

	require.NotNil(t, tr.Steps[0].Input)
	assert.Equal(t, "user input", tr.Steps[0].Input.Text)
	require.NotNil(t, tr.Steps[0].Output)
	assert.Equal(t, "model output", tr.Steps[0].Output.Text)

	require.NotNil(t, tr.Steps[1].Input)
	assert.Equal(t, "tool input", tr.Steps[1].Input.Text)
}

func TestStepDAG_NilInputOutput(t *testing.T) {
	reporter := &mockReporter{}
	ts := newTestPlugin(reporter, Config{Enabled: true, SampleRate: 1})

	execTrace := &agenttrace.Trace{
		Status: agenttrace.TraceStatusCompleted,
		Steps: []agenttrace.Step{
			{
				StepID: "step-1",
				NodeID: "node-1",
				Input:  nil, // explicitly nil
				Output: nil,
			},
		},
	}
	inv := rootInvocation()
	ctx := context.Background()
	afterArgs := &agent.AfterAgentArgs{
		FullResponseEvent: makeFullResponseEvent(execTrace, "ok"),
	}
	runFullCycle(ctx, ts, inv, afterArgs)

	require.Equal(t, 1, reporter.count())
	tr := reporter.lastTrace()
	assert.Nil(t, tr.Steps[0].Input)
	assert.Nil(t, tr.Steps[0].Output)
}

// ===================================================================
// 4.5 Config hot-update tests
// ===================================================================

func TestUpdateConfig_ImmediateEffect(t *testing.T) {
	reporter := &mockReporter{}
	ts := newTestPlugin(reporter, Config{Enabled: false, SampleRate: 0})

	inv := rootInvocation()

	// First run: disabled → should not report.
	ctx := context.Background()
	afterArgs := &agent.AfterAgentArgs{
		FullResponseEvent: makeFullResponseEvent(sampleExecutionTrace(), "hello"),
	}
	runFullCycle(ctx, ts, inv, afterArgs)
	assert.Equal(t, 0, reporter.count())

	// Hot-update to enabled.
	ts.UpdateConfig(Config{Enabled: true, SampleRate: 1})

	// Second run: should now report.
	ctx2 := context.Background()
	afterArgs2 := &agent.AfterAgentArgs{
		FullResponseEvent: makeFullResponseEvent(sampleExecutionTrace(), "hello"),
	}
	runFullCycle(ctx2, ts, inv, afterArgs2)
	assert.Equal(t, 1, reporter.count())
}

func TestUpdateConfig_DisableAtRuntime(t *testing.T) {
	reporter := &mockReporter{}
	ts := newTestPlugin(reporter, Config{Enabled: true, SampleRate: 1})

	inv := rootInvocation()

	// First run: enabled → should report.
	ctx := context.Background()
	afterArgs := &agent.AfterAgentArgs{
		FullResponseEvent: makeFullResponseEvent(sampleExecutionTrace(), "hello"),
	}
	runFullCycle(ctx, ts, inv, afterArgs)
	assert.Equal(t, 1, reporter.count())

	// Hot-update to disabled.
	ts.UpdateConfig(Config{Enabled: false})

	// Second run: disabled → should not report.
	ctx2 := context.Background()
	afterArgs2 := &agent.AfterAgentArgs{
		FullResponseEvent: makeFullResponseEvent(sampleExecutionTrace(), "hello"),
	}
	runFullCycle(ctx2, ts, inv, afterArgs2)
	assert.Equal(t, 1, reporter.count(), "should still be 1 after disabling")
}

// ===================================================================
// 4.6 Reporter error handling tests
// ===================================================================

func TestReporter_Error_NotPropagated(t *testing.T) {
	reporter := &mockReporter{err: errors.New("report failed")}
	ts := newTestPlugin(reporter, Config{Enabled: true, SampleRate: 1})

	inv := rootInvocation()
	ctx := context.Background()

	// BeforeAgent
	beforeResult, beforeErr := ts.beforeAgent(ctx, &agent.BeforeAgentArgs{
		Invocation: inv,
	})
	require.NoError(t, beforeErr)
	if beforeResult != nil && beforeResult.Context != nil {
		ctx = beforeResult.Context
	}

	// AfterAgent — reporter returns an error, but the callback must not
	// propagate it.
	afterArgs := &agent.AfterAgentArgs{
		Invocation:        inv,
		FullResponseEvent: makeFullResponseEvent(sampleExecutionTrace(), "hello"),
	}
	result, err := ts.afterAgent(ctx, afterArgs)
	assert.NoError(t, err, "reporter error must not propagate")
	assert.Nil(t, result)

	// The reporter was still called.
	assert.Equal(t, 1, reporter.count())
}

func TestReporter_Nil_NoPanic(t *testing.T) {
	// Create plugin without a Reporter.
	ts := New(
		WithConfig(Config{Enabled: true, SampleRate: 1}),
	)

	inv := rootInvocation()
	ctx := context.Background()

	// Should not panic.
	afterArgs := &agent.AfterAgentArgs{
		FullResponseEvent: makeFullResponseEvent(sampleExecutionTrace(), "hello"),
	}
	assert.NotPanics(t, func() {
		runFullCycle(ctx, ts, inv, afterArgs)
	})
}

// ===================================================================
// Root Agent idempotency tests
// ===================================================================

func TestNestedAgent_NotReported(t *testing.T) {
	reporter := &mockReporter{}
	ts := newTestPlugin(reporter, Config{Enabled: true, SampleRate: 1})

	rootInv := rootInvocation()
	childInv := nestedInvocation()

	ctx := context.Background()

	// Root BeforeAgent.
	result, _ := ts.beforeAgent(ctx, &agent.BeforeAgentArgs{Invocation: rootInv})
	if result != nil && result.Context != nil {
		ctx = result.Context
	}

	// Child BeforeAgent — should detect existing context mark.
	childResult, _ := ts.beforeAgent(ctx, &agent.BeforeAgentArgs{Invocation: childInv})
	if childResult != nil && childResult.Context != nil {
		ctx = childResult.Context
	}

	// Child AfterAgent — nested, should not report.
	ts.afterAgent(ctx, &agent.AfterAgentArgs{ //nolint: errcheck
		Invocation:        childInv,
		FullResponseEvent: makeFullResponseEvent(sampleExecutionTrace(), "child output"),
	})
	assert.Equal(t, 0, reporter.count(), "nested agent must not trigger report")

	// Root AfterAgent — should report.
	ts.afterAgent(ctx, &agent.AfterAgentArgs{ //nolint: errcheck
		Invocation:        rootInv,
		FullResponseEvent: makeFullResponseEvent(sampleExecutionTrace(), "root output"),
	})
	assert.Equal(t, 1, reporter.count(), "only root agent should trigger report")
}

func TestBeforeAgent_NilArgs(t *testing.T) {
	ts := New(WithConfig(Config{Enabled: true, SampleRate: 1}))

	result, err := ts.beforeAgent(context.Background(), nil)
	assert.NoError(t, err)
	assert.Nil(t, result)
}

func TestAfterAgent_NilArgs(t *testing.T) {
	ts := New(WithConfig(Config{Enabled: true, SampleRate: 1}))

	result, err := ts.afterAgent(context.Background(), nil)
	assert.NoError(t, err)
	assert.Nil(t, result)
}
