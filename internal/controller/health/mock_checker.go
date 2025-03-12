package health

import "context"

// MockChecker is a mock implementation of the Checker interface, which can be
// used for testing.
type MockChecker struct {
	// CheckName is the name of the Checker.
	CheckName string
	// CheckFunc is the function that this Checker should call when Check is
	// called. If set, this function will be called instead of returning
	// checkResult.
	CheckFunc func(context.Context, Input) Result
	// CheckResult is the result that this Checker should return when Check is
	// called.
	CheckResult Result
}

// Name implements the Checker interface.
func (m *MockChecker) Name() string {
	return m.CheckName
}

// Check implements the Checker interface.
func (m *MockChecker) Check(ctx context.Context, input Input) Result {
	if m.CheckFunc != nil {
		return m.CheckFunc(ctx, input)
	}
	return m.CheckResult
}
