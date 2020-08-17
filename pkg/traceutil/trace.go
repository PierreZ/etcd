// Copyright 2019 The etcd Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package traceutil implements tracing utilities using "context".
package traceutil

import (
	"bytes"
	"context"
	"fmt"
	"math/rand"
	"sync"
	"time"

	"go.uber.org/zap"
)

const (
	TraceKey     = "trace"
	StartTimeKey = "startTime"
)

// Field is a kv pair to record additional details of the trace.
type Field struct {
	Key   string
	Value interface{}
}

func (f *Field) format() string {
	return fmt.Sprintf("%s:%v; ", f.Key, f.Value)
}

func writeFields(fields []Field) string {
	if len(fields) == 0 {
		return ""
	}
	var buf bytes.Buffer
	buf.WriteString("{")
	for _, f := range fields {
		buf.WriteString(f.format())
	}
	buf.WriteString("}")
	return buf.String()
}

// Trace is safe to be shared between goroutines
type Trace struct {
	sync.RWMutex
	operation    string
	lg           *zap.Logger
	fields       []Field
	startTime    time.Time
	steps        []step
	stepDisabled bool
}

type step struct {
	time   time.Time
	msg    string
	fields []Field
}

func New(op string, lg *zap.Logger, fields ...Field) *Trace {
	return &Trace{operation: op, lg: lg, startTime: time.Now(), fields: fields}
}

// TODO returns a non-nil, empty Trace
func TODO() *Trace {
	return &Trace{}
}

func Get(ctx context.Context) *Trace {
	if trace, ok := ctx.Value(TraceKey).(*Trace); ok && trace != nil {
		return trace
	}
	return TODO()
}

func (t *Trace) GetStartTime() time.Time {
	t.RLock()
	defer t.RUnlock()
	return t.startTime
}

func (t *Trace) SetStartTime(time time.Time) {
	t.Lock()
	defer t.Unlock()
	t.startTime = time
}

func (t *Trace) InsertStep(at int, time time.Time, msg string, fields ...Field) {
	t.Lock()
	defer t.Unlock()
	newStep := step{time: time, msg: msg, fields: fields}
	if at < len(t.steps) {
		t.steps = append(t.steps[:at+1], t.steps[at:]...)
		t.steps[at] = newStep
	} else {
		t.steps = append(t.steps, newStep)
	}
}

// Step adds step to trace
func (t *Trace) Step(msg string, fields ...Field) {
	t.Lock()
	defer t.Unlock()
	if !t.stepDisabled {
		t.steps = append(t.steps, step{time: time.Now(), msg: msg, fields: fields})
	}
}

// DisableStep sets the flag to prevent the trace from adding steps
func (t *Trace) DisableStep() {
	t.Lock()
	defer t.Unlock()
	t.stepDisabled = true
}

// EnableStep re-enable the trace to add steps
func (t *Trace) EnableStep() {
	t.Lock()
	defer t.Unlock()
	t.stepDisabled = false
}

func (t *Trace) AddField(fields ...Field) {
	t.Lock()
	defer t.Unlock()
	for _, f := range fields {
		t.fields = append(t.fields, f)
	}
}

// Log dumps all steps in the Trace
func (t *Trace) Log() {
	t.LogWithStepThreshold(0)
}

// LogIfLong dumps logs if the duration is longer than threshold
func (t *Trace) LogIfLong(threshold time.Duration) {
	if time.Since(t.startTime) > threshold {
		stepThreshold := threshold / time.Duration(len(t.steps)+1)
		t.LogWithStepThreshold(stepThreshold)
	}
}

// LogWithStepThreshold only dumps step whose duration is longer than step threshold
func (t *Trace) LogWithStepThreshold(threshold time.Duration) {
	msg, fs := t.logInfo(threshold)
	if t.lg != nil {
		t.lg.Info(msg, fs...)
	}
}

func (t *Trace) logInfo(threshold time.Duration) (string, []zap.Field) {
	t.Lock()
	defer t.Unlock()
	endTime := time.Now()
	totalDuration := endTime.Sub(t.startTime)
	traceNum := rand.Int31()
	msg := fmt.Sprintf("trace[%d] %s", traceNum, t.operation)

	var steps []string
	lastStepTime := t.startTime
	for _, step := range t.steps {
		stepDuration := step.time.Sub(lastStepTime)
		if stepDuration > threshold {
			steps = append(steps, fmt.Sprintf("trace[%d] '%v' %s (duration: %v)",
				traceNum, step.msg, writeFields(step.fields), stepDuration))
		}
		lastStepTime = step.time
	}

	fs := []zap.Field{zap.String("detail", writeFields(t.fields)),
		zap.Duration("duration", totalDuration),
		zap.Time("start", t.startTime),
		zap.Time("end", endTime),
		zap.Strings("steps", steps)}
	return msg, fs
}
