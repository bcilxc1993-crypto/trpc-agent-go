//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package tracesampler provides a plugin that aggregates a complete Runner
// execution into a single PromptIter-compatible Trace and reports it via a
// user-supplied Reporter.
package tracesampler

// TraceStatus describes the overall status of a Trace.
type TraceStatus string

const (
	// TraceStatusCompleted indicates the run completed successfully.
	TraceStatusCompleted TraceStatus = "completed"
	// TraceStatusIncomplete indicates the run produced no execution trace.
	TraceStatusIncomplete TraceStatus = "incomplete"
	// TraceStatusFailed indicates the run ended with a terminal error.
	TraceStatusFailed TraceStatus = "failed"
)

// Trace is the PromptIter-compatible execution trace for a single Runner run.
// It aligns with the Trace structure defined in the PromptIter design document
// (section 4.3).
type Trace struct {
	// StructureID identifies the StructureSnapshot that was active during
	// this execution. Injected via WithStructureID or UpdateConfig.
	StructureID string

	// Status is the overall execution status.
	Status TraceStatus

	// FinalOutput contains the final response text of the run.
	FinalOutput *TraceOutput

	// Steps is the ordered list of execution steps forming a step-DAG.
	Steps []TraceStep
}

// TraceStep represents a single execution step within a Trace.
type TraceStep struct {
	// StepID uniquely identifies this step within the Trace.
	StepID string

	// NodeID identifies the static graph node (if any) that produced this step.
	NodeID string

	// PredecessorStepIDs lists the StepIDs of direct predecessors in the
	// step-DAG.
	PredecessorStepIDs []string

	// AppliedSurfaceIDs lists the PromptIter surface IDs that were applied
	// to this step. May be nil if the framework does not yet track surfaces.
	AppliedSurfaceIDs []string

	// Input is the text snapshot captured before this step executed.
	Input *TraceInput

	// Output is the text snapshot captured after this step executed.
	Output *TraceOutput

	// Error is a non-empty string if this step ended with an error.
	Error string
}

// TraceInput stores a text snapshot for a step's input.
type TraceInput struct {
	Text string
}

// TraceOutput stores a text snapshot for a step's output.
type TraceOutput struct {
	Text string
}
